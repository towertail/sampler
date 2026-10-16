//go:build !windows

package scan

import (
	"os"
	"syscall"
)

// stat is what the walk needs to know about one entry.
type stat struct {
	alloc   int64
	dev     uint64
	ino     uint64
	nlink   uint64
	mtime   int64
	dir     bool
	regular bool
}

// lstat reads an entry without following a symlink. alloc is the
// allocated size, so a sparse file counts only its used blocks.
func lstat(path string) (stat, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return stat{}, err
	}
	s := stat{mtime: fi.ModTime().Unix(), dir: fi.IsDir(), regular: fi.Mode().IsRegular()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		s.alloc = int64(st.Blocks) * 512
		s.dev = uint64(st.Dev)
		s.ino = uint64(st.Ino)
		s.nlink = uint64(st.Nlink)
	} else {
		s.alloc = fi.Size()
	}
	return s, nil
}
