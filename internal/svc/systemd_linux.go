//go:build linux

package svc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type systemd struct {
	system bool
}

func platformNewManager(opts Options) (Manager, error) {
	return &systemd{system: opts.System}, nil
}

const systemdUnit = "towertail-sampler.service"

const unitTemplate = `[Unit]
Description=Towertail Sampler
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{arg .ExecPath}} push --endpoint {{arg .Endpoint}} --token-file {{arg .TokenFile}} --interval {{.Interval}}{{if .AllowControl}} --allow-control{{end}}
Restart=on-failure
RestartSec=5
{{- if .User }}
User={{.User}}
{{- end }}
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome={{.ProtectHome}}
PrivateTmp=yes

[Install]
WantedBy={{.WantedBy}}
`

type unitCtx struct {
	ExecPath     string
	Endpoint     string
	TokenFile    string
	Interval     string
	User         string
	AllowControl bool
	WantedBy     string
	ProtectHome  string
}

var unitTmpl = template.Must(template.New("unit").Funcs(template.FuncMap{"arg": unitArg}).Parse(unitTemplate))

// unitArg quotes s for a systemd ExecStart line and escapes "%"
// specifiers.
func unitArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\\\"'$;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$")
	return `"` + r.Replace(s) + `"`
}

// underHome reports if path is in a directory that ProtectHome=yes
// hides.
func underHome(path string) bool {
	for _, dir := range []string{"/home", "/root", "/run/user"} {
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// renderUnit returns the unit file text. A user unit reads its binary
// and token under $HOME, so it gets ProtectHome=read-only. A system
// unit gets ProtectHome=yes only if no file it needs is under a home
// directory.
func (s *systemd) renderUnit(cfg InstallConfig, tokenFile string) ([]byte, error) {
	wantedBy := "default.target"
	protectHome := "read-only"
	if s.system {
		wantedBy = "multi-user.target"
		if !underHome(cfg.ExecPath) && !underHome(tokenFile) {
			protectHome = "yes"
		}
	}
	var buf bytes.Buffer
	err := unitTmpl.Execute(&buf, unitCtx{
		ExecPath:     cfg.ExecPath,
		Endpoint:     cfg.Endpoint,
		TokenFile:    tokenFile,
		Interval:     cfg.Interval.String(),
		User:         cfg.User,
		AllowControl: cfg.AllowControl,
		WantedBy:     wantedBy,
		ProtectHome:  protectHome,
	})
	return buf.Bytes(), err
}

func (s *systemd) unitPath() (string, error) {
	if s.system {
		return "/etc/systemd/system/" + systemdUnit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config/systemd/user", systemdUnit), nil
}

func (s *systemd) tokenFilePath() (string, error) {
	if s.system {
		return "/etc/towertail/sampler.token", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config/towertail/sampler.token"), nil
}

func (s *systemd) systemctlArgs(extra ...string) []string {
	base := []string{}
	if !s.system {
		base = append(base, "--user")
	}
	return append(base, extra...)
}

func (s *systemd) Install(ctx context.Context, cfg InstallConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	unitPath, err := s.unitPath()
	if err != nil {
		return err
	}
	tokenFile, err := s.tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tokenFile, []byte(cfg.Token+"\n"), 0o600); err != nil {
		return err
	}
	unit, err := s.renderUnit(cfg, tokenFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, unit, 0o644); err != nil {
		return err
	}
	if err := runCmd(ctx, "systemctl", s.systemctlArgs("daemon-reload")...); err != nil {
		return err
	}
	if err := runCmd(ctx, "systemctl", s.systemctlArgs("enable", systemdUnit)...); err != nil {
		return err
	}
	return runCmd(ctx, "systemctl", s.systemctlArgs("start", systemdUnit)...)
}

func (s *systemd) Uninstall(ctx context.Context) error {
	_ = runCmd(ctx, "systemctl", s.systemctlArgs("stop", systemdUnit)...)
	_ = runCmd(ctx, "systemctl", s.systemctlArgs("disable", systemdUnit)...)
	unitPath, err := s.unitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = runCmd(ctx, "systemctl", s.systemctlArgs("daemon-reload")...)
	tokenFile, err := s.tokenFilePath()
	if err != nil {
		return err
	}
	_ = os.Remove(tokenFile)
	return nil
}

func (s *systemd) Start(ctx context.Context) error {
	return runCmd(ctx, "systemctl", s.systemctlArgs("start", systemdUnit)...)
}

func (s *systemd) Stop(ctx context.Context) error {
	return runCmd(ctx, "systemctl", s.systemctlArgs("stop", systemdUnit)...)
}

func (s *systemd) Status(ctx context.Context) (string, error) {
	out, _ := exec.CommandContext(ctx, "systemctl", append(s.systemctlArgs("is-active"), systemdUnit)...).CombinedOutput()
	return strings.TrimSpace(string(out)), nil
}

func (s *systemd) Log(ctx context.Context, follow bool, w io.Writer) error {
	args := []string{"-u", systemdUnit}
	if !s.system {
		args = append([]string{"--user-unit", systemdUnit}, args...)
	}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
