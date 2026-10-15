//go:build !windows

package collect

import "os"

// isElevated reports whether the sampler is running with the privileged
// regime for this OS — euid 0 on Unix. On Linux this is what lets
// `/proc/<pid>/io` for other users' processes be read; on macOS it's
// what lets `sysctl kinfo_proc` return other users' entries.
func isElevated() bool {
	return os.Geteuid() == 0
}
