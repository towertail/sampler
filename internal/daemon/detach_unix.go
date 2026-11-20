//go:build !windows

package daemon

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Detach starts exe with args in a new session, with no stdio. It waits
// up to wait until the new process holds the lock and has written its
// pid.
func Detach(exe string, args []string, h Home, wait time.Duration) (pid int, err error) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return h.waitRunning(wait, cmd.Process.Pid, exited)
}

// WaitRunning waits up to wait until a process holds the lock.
func (h Home) WaitRunning(wait time.Duration) (pid int, err error) {
	return h.waitRunning(wait, 0, nil)
}

// waitRunning polls the lock. A wantPID of 0 accepts any pid. When
// exited closes, the process ended: it counts as started only when
// another process holds the lock (a second start does nothing).
func (h Home) waitRunning(wait time.Duration, wantPID int, exited <-chan struct{}) (int, error) {
	deadline := time.Now().Add(wait)
	for {
		select {
		case <-exited:
			if pid, ok := h.Running(); ok {
				return pid, nil
			}
			return 0, fmt.Errorf("%w: process ended\n%s", ErrStartTimeout, h.logTail(20))
		default:
		}
		if pid, ok := h.Running(); ok && (wantPID == 0 || pid == wantPID) {
			return pid, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("%w: no lock after %s\n%s", ErrStartTimeout, wait, h.logTail(20))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logTail returns the last n lines of the log.
func (h Home) logTail(n int) string {
	b, err := os.ReadFile(h.LogPath())
	if err != nil {
		return ""
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(string(bytes.Join(lines, []byte("\n"))))
}
