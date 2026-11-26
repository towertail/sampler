// Package boot makes the sampler start again after a reboot. It sets
// one of three entries: a systemd user unit with linger, a macOS
// LaunchAgent, or crontab lines. None of them needs root.
package boot

import (
	"bytes"
	"context"
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
