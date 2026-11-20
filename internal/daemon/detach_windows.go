package daemon

import "time"

func Detach(exe string, args []string, h Home, wait time.Duration) (pid int, err error) {
	return 0, ErrUnsupported
}

func (h Home) WaitRunning(wait time.Duration) (pid int, err error) {
	return 0, ErrUnsupported
}
