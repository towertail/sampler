package scan

import "golang.org/x/sys/windows"

// LowerPriority puts the process in background mode. Windows then
// lowers its CPU, I/O and memory priority.
func LowerPriority() error {
	return windows.SetPriorityClass(windows.CurrentProcess(), windows.PROCESS_MODE_BACKGROUND_BEGIN)
}
