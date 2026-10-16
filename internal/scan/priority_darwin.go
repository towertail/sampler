package scan

import "syscall"

// LowerPriority sets nice 10 for the process.
func LowerPriority() error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
}
