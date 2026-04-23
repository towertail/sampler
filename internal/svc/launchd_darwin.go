//go:build darwin

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

type launchd struct {
	system bool
}

func platformNewManager(opts Options) (Manager, error) {
	return &launchd{system: opts.System}, nil
}

const launchdLabel = "com.towertail.sampler"

func (l *launchd) plistPath() (string, error) {
	if l.system {
		return "/Library/LaunchDaemons/" + launchdLabel + ".plist", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library/LaunchAgents", launchdLabel+".plist"), nil
}

func (l *launchd) logPath() (string, error) {
	if l.system {
		return "/var/log/towertail-sampler.log", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library/Logs/Towertail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "sampler.log"), nil
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
      <string>{{.ExecPath}}</string>
      <string>push</string>
      <string>--endpoint</string>
      <string>{{.Endpoint}}</string>
      <string>--token-file</string>
      <string>{{.TokenFile}}</string>
      <string>--interval</string>
      <string>{{.Interval}}</string>
      {{- if .AllowControl }}
      <string>--allow-control</string>
      {{- end }}
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>{{.LogPath}}</string>
    <key>StandardErrorPath</key><string>{{.LogPath}}</string>
  </dict>
</plist>
`

type plistCtx struct {
	Label        string
	ExecPath     string
	Endpoint     string
	TokenFile    string
	Interval     string
	LogPath      string
	AllowControl bool
}

func (l *launchd) Install(ctx context.Context, cfg InstallConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	plist, err := l.plistPath()
	if err != nil {
		return err
	}
	tokenFile, err := l.tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tokenFile, []byte(cfg.Token+"\n"), 0o600); err != nil {
		return err
	}
	logPath, err := l.logPath()
	if err != nil {
		return err
	}
	tmpl := template.Must(template.New("plist").Parse(plistTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, plistCtx{
		Label:        launchdLabel,
		ExecPath:     cfg.ExecPath,
		Endpoint:     cfg.Endpoint,
		TokenFile:    tokenFile,
		Interval:     cfg.Interval.String(),
		LogPath:      logPath,
		AllowControl: cfg.AllowControl,
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plist, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := runCmd(ctx, "launchctl", "load", "-w", plist); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

func (l *launchd) Uninstall(ctx context.Context) error {
	plist, err := l.plistPath()
	if err != nil {
		return err
	}
	_ = runCmd(ctx, "launchctl", "unload", plist)
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return err
	}
	tokenFile, err := l.tokenFilePath()
	if err != nil {
		return err
	}
	_ = os.Remove(tokenFile)
	return nil
}

func (l *launchd) Start(ctx context.Context) error {
	return runCmd(ctx, "launchctl", "start", launchdLabel)
}

func (l *launchd) Stop(ctx context.Context) error {
	return runCmd(ctx, "launchctl", "stop", launchdLabel)
}

func (l *launchd) Status(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "launchctl", "list", launchdLabel).CombinedOutput()
	if err != nil {
		return "not installed", nil
	}
	return strings.TrimSpace(string(out)), nil
}

func (l *launchd) Log(ctx context.Context, follow bool, w io.Writer) error {
	logPath, err := l.logPath()
	if err != nil {
		return err
	}
	args := []string{}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, logPath)
	cmd := exec.CommandContext(ctx, "/usr/bin/tail", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}

func (l *launchd) tokenFilePath() (string, error) {
	if l.system {
		return "/etc/towertail/sampler.token", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library/Application Support/Towertail/sampler.token"), nil
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
