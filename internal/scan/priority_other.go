//go:build !linux && !darwin && !windows

package scan

// LowerPriority does nothing on this OS.
func LowerPriority() error { return nil }
