package scan

import (
	"os"
	"strconv"
	"syscall"
)

const (
	ioprioWhoProcess = 1
	ioprioClassIdle  = 3
	ioprioClassShift = 13
)

// LowerPriority sets nice 10 and the idle I/O class. On Linux both are
// per thread, so it sets each thread of the process. Threads that start
// later get the values of the thread that starts them.
func LowerPriority() error {
	tids, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return err
	}
	var first error
	for _, t := range tids {
		tid, err := strconv.Atoi(t.Name())
		if err != nil {
			continue
		}
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, tid, 10); err != nil && first == nil {
			first = err
		}
		_, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioClassIdle<<ioprioClassShift)
		if errno != 0 && first == nil {
			first = errno
		}
	}
	return first
}
