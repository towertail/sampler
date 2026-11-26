package daemon

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Terminate sends SIGTERM to the running process and waits up to wait
// for it to free the lock. Then it sends SIGKILL. It returns nil when no
// process runs.
func (h Home) Terminate(wait time.Duration) error {
	pid, ok := h.Running()
	if !ok {
		return nil
	}
	if pid <= 1 {
		return fmt.Errorf("sampler runs, but the pid file has no pid")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	if h.waitFree(wait) {
		return nil
	}
	if err := proc.Kill(); err != nil {
		return err
	}
	if !h.waitFree(5 * time.Second) {
		return fmt.Errorf("sampler pid %d did not stop", pid)
	}
	return nil
}

func (h Home) waitFree(wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if _, ok := h.Running(); !ok {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
