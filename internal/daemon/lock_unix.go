//go:build !windows

package daemon

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Lock takes the lock of the sampler process. It returns ErrRunning
// when another process holds it.
func (h Home) Lock() (*Lock, error) {
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(h.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() error {
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	return l.f.Close()
}
