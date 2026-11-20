package daemon

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Lock is the held lock of a running sampler process.
type Lock struct {
	f *os.File
}

// Running tells if a process holds the lock, and gives its pid. A stale
// pid file with a free lock gives ok=false. The pid is 0 when the pid
// file is not readable.
func (h Home) Running() (pid int, ok bool) {
	l, err := h.Lock()
	if err == nil {
		_ = l.Release()
		return 0, false
	}
	if !errors.Is(err, ErrRunning) {
		return 0, false
	}
	b, err := os.ReadFile(h.PIDPath())
	if err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	return pid, true
}

// LockWait takes the lock like Lock. It tries again on ErrRunning for
// up to wait, because a probe of Running holds the lock for a moment.
func (h Home) LockWait(wait time.Duration) (*Lock, error) {
	deadline := time.Now().Add(wait)
	for {
		l, err := h.Lock()
		if !errors.Is(err, ErrRunning) || time.Now().After(deadline) {
			return l, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
