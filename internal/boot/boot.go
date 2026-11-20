// Package boot makes the sampler start again after a reboot. It sets
// one of three entries: a systemd user unit with linger, a macOS
// LaunchAgent, or crontab lines. None of them needs root.
package boot

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/towertail/sampler/internal/daemon"
)

const (
	unitName = "towertail-sampler.service"
	label    = "com.towertail.sampler"
	// marker ends each crontab line of the sampler.
	marker = "# towertail-sampler"
)

var (
	// ErrNoBootMethod means that no entry could be set.
	ErrNoBootMethod = errors.New("no way to start the sampler after a reboot")
	ErrUnsupported  = daemon.ErrUnsupported
)

// Runner runs a system command. Tests give a fake.
type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader) (stdout []byte, err error)
}

// ExecRunner runs commands with os/exec. The error holds the stderr text.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Entry sets, removes and detects the reboot entry of one user.
type Entry struct {
	home     daemon.Home
	run      Runner
	user     string
	uid      int
	goos     string
	unitDir  string
	agentDir string
}

func NewEntry(h daemon.Home, r Runner) *Entry {
	e := &Entry{home: h, run: r, uid: os.Getuid(), goos: runtime.GOOS}
	if u, err := user.Current(); err == nil {
		e.user = u.Username
	}
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	e.unitDir = filepath.Join(cfg, "systemd", "user")
	e.agentDir = filepath.Join(home, "Library", "LaunchAgents")
	return e
}

func (e *Entry) unitPath() string  { return filepath.Join(e.unitDir, unitName) }
func (e *Entry) plistPath() string { return filepath.Join(e.agentDir, label+".plist") }

func renderUnit(bin string) string {
	return `[Unit]
Description=Towertail sampler

[Service]
Type=simple
ExecStart=` + bin + ` start --foreground
Restart=on-failure
RestartSec=10
StandardOutput=null
StandardError=null

[Install]
WantedBy=default.target
`
}

func renderPlist(bin string) string {
	var esc bytes.Buffer
	_ = xml.EscapeText(&esc, []byte(bin))
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc.String() + `</string>
		<string>start</string>
		<string>--foreground</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
</dict>
</plist>
`
}

// editCrontab removes the marked lines of old. With add, it then adds
// the two lines of the sampler.
func editCrontab(old string, bin string, add bool) string {
	var lines []string
	if strings.TrimSpace(old) != "" {
		for _, l := range strings.Split(strings.TrimRight(old, "\n"), "\n") {
			if !strings.Contains(l, marker) {
				lines = append(lines, l)
			}
		}
	}
	if add {
		cmd := bin + " start >/dev/null 2>&1 " + marker
		lines = append(lines, "@reboot "+cmd, "*/5 * * * * "+cmd)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
