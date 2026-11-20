//go:build !windows

package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Enable sets the reboot entry and returns the mode that it set. The
// mode is auto, linger, launchagent or cron. Auto tries the best entry
// of the OS first and falls back to cron.
func (e *Entry) Enable(ctx context.Context, mode string) (got string, err error) {
	switch mode {
	case "linger":
		return e.only("linger", e.enableLinger(ctx))
	case "launchagent":
		return e.only("launchagent", e.enableLaunchAgent(ctx))
	case "cron":
		return e.only("cron", e.enableCron(ctx))
	case "auto":
		if e.goos == "darwin" {
			return e.autoDarwin(ctx)
		}
		if err := e.enableLinger(ctx); err == nil {
			return "linger", nil
		}
		return e.cronFallback(ctx)
	}
	return "", fmt.Errorf("unknown boot mode %q", mode)
}

func (e *Entry) only(mode string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return mode, nil
}

func (e *Entry) cronFallback(ctx context.Context) (string, error) {
	if err := e.enableCron(ctx); err != nil {
		return "", fmt.Errorf("%w (%v)", ErrNoBootMethod, err)
	}
	return "cron", nil
}

// autoDarwin sets the LaunchAgent. It starts only after a login, so a
// Mac with no auto-login gets the cron lines too.
func (e *Entry) autoDarwin(ctx context.Context) (string, error) {
	if err := e.enableLaunchAgent(ctx); err != nil {
		return e.cronFallback(ctx)
	}
	if _, err := e.run.Run(ctx, "defaults", []string{"read", "/Library/Preferences/com.apple.loginwindow", "autoLoginUser"}, nil); err == nil {
		return "launchagent", nil
	}
	if err := e.enableCron(ctx); err != nil {
		return "launchagent", nil
	}
	return "launchagent+cron", nil
}

func (e *Entry) systemctl(ctx context.Context, args ...string) error {
	_, err := e.run.Run(ctx, "systemctl", append([]string{"--user"}, args...), nil)
	return err
}

func (e *Entry) enableLinger(ctx context.Context) error {
	if e.goos != "linux" {
		return errors.New("linger works on Linux only")
	}
	if err := e.systemctl(ctx, "show-environment"); err != nil {
		return fmt.Errorf("no systemd user manager: %w", err)
	}
	if err := os.MkdirAll(e.unitDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(e.unitPath(), []byte(renderUnit(e.home.Bin())), 0o644); err != nil {
		return err
	}
	err := e.systemctl(ctx, "daemon-reload")
	if err == nil {
		err = e.systemctl(ctx, "enable", unitName)
	}
	if err == nil {
		_, err = e.run.Run(ctx, "loginctl", []string{"enable-linger", e.user}, nil)
	}
	if err != nil {
		_ = e.removeUnit(ctx)
		return err
	}
	return nil
}

// removeUnit turns off the unit and removes its file. It does not turn
// off linger, because other user services can need it. It does not stop
// the unit, because the running process can call it on itself.
func (e *Entry) removeUnit(ctx context.Context) error {
	_ = e.systemctl(ctx, "disable", unitName)
	if err := os.Remove(e.unitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = e.systemctl(ctx, "daemon-reload")
	return nil
}

type exitCoder interface{ ExitCode() int }

func exitCode(err error) int {
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

func (e *Entry) enableLaunchAgent(ctx context.Context) error {
	if e.goos != "darwin" {
		return errors.New("launchagent works on macOS only")
	}
	if err := os.MkdirAll(e.agentDir, 0o755); err != nil {
		return err
	}
	plist := e.plistPath()
	if err := os.WriteFile(plist, []byte(renderPlist(e.home.Bin())), 0o644); err != nil {
		return err
	}
	err := e.bootstrap(ctx, "gui", plist)
	// 125 and 5: the Mac has no GUI session (no user is logged in).
	if c := exitCode(err); err != nil && (c == 125 || c == 5) {
		err = e.bootstrap(ctx, "user", plist)
	}
	if err != nil && !e.loaded(ctx) {
		_ = os.Remove(plist)
		return err
	}
	return nil
}

func (e *Entry) bootstrap(ctx context.Context, domain, plist string) error {
	_, err := e.run.Run(ctx, "launchctl", []string{"bootstrap", fmt.Sprintf("%s/%d", domain, e.uid), plist}, nil)
	return err
}

// loaded tells if launchd knows the agent in the GUI or user domain.
func (e *Entry) loaded(ctx context.Context) bool {
	for _, domain := range []string{"gui", "user"} {
		target := fmt.Sprintf("%s/%d/%s", domain, e.uid, label)
		if _, err := e.run.Run(ctx, "launchctl", []string{"print", target}, nil); err == nil {
			return true
		}
	}
	return false
}

// readCrontab returns the crontab. No crontab is an empty string.
func (e *Entry) readCrontab(ctx context.Context) (string, error) {
	out, err := e.run.Run(ctx, "crontab", []string{"-l"}, nil)
	if err != nil && strings.Contains(err.Error(), "no crontab") {
		return "", nil
	}
	return string(out), err
}

func (e *Entry) writeCrontab(ctx context.Context, text string) error {
	if text == "" {
		_, err := e.run.Run(ctx, "crontab", []string{"-r"}, nil)
		return err
	}
	_, err := e.run.Run(ctx, "crontab", []string{"-"}, strings.NewReader(text))
	return err
}

func (e *Entry) enableCron(ctx context.Context) error {
	old, err := e.readCrontab(ctx)
	if err != nil {
		return err
	}
	return e.writeCrontab(ctx, editCrontab(old, e.home.Bin(), true))
}

// Disable removes each kind of entry that exists. It does not stop the
// running process and does not run launchctl bootout, because the
// process can call Disable on itself.
func (e *Entry) Disable(ctx context.Context) error {
	var errs []error
	if err := os.Remove(e.plistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if _, err := os.Stat(e.unitPath()); err == nil {
		if err := e.removeUnit(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	old, err := e.readCrontab(ctx)
	switch {
	case errors.Is(err, exec.ErrNotFound):
	case err != nil:
		errs = append(errs, err)
	case strings.Contains(old, marker):
		if err := e.writeCrontab(ctx, editCrontab(old, e.home.Bin(), false)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Detect returns the entry that works: linger, launchagent,
// launchagent+cron, cron or none.
func (e *Entry) Detect(ctx context.Context) string {
	cron := false
	if old, err := e.readCrontab(ctx); err == nil {
		cron = strings.Contains(old, marker)
	}
	if fileExists(e.plistPath()) && e.loaded(ctx) {
		if cron {
			return "launchagent+cron"
		}
		return "launchagent"
	}
	if fileExists(e.unitPath()) {
		out, err := e.run.Run(ctx, "loginctl", []string{"show-user", e.user, "-p", "Linger"}, nil)
		if err == nil && strings.Contains(string(out), "Linger=yes") {
			return "linger"
		}
	}
	if cron {
		return "cron"
	}
	return "none"
}

func fileExists(p string) bool {
	_, err := os.Stat(filepath.Clean(p))
	return err == nil
}

// StartService starts the systemd unit in the user manager.
func (e *Entry) StartService(ctx context.Context) error {
	return e.systemctl(ctx, "start", unitName)
}

// StopService stops the systemd unit.
func (e *Entry) StopService(ctx context.Context) error {
	return e.systemctl(ctx, "stop", unitName)
}
