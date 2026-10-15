//go:build windows

package collect

import "golang.org/x/sys/windows"

// isElevated reports whether the current process token is elevated —
// the Windows equivalent of "am I root?". Checks TokenElevation on the
// current process token. Returns false on any error (a non-elevated
// user may be denied the query on hardened systems; treat that as
// "not elevated" for reporting purposes).
func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}
