//go:build windows

package scan

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetDiskFreeSpace = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceW")

type stat struct {
	alloc   int64
	dev     uint64
	ino     uint64
	nlink   uint64
	mtime   int64
	dir     bool
	regular bool
}

var clusters sync.Map

// clusterSize returns the cluster size of the volume that holds path.
func clusterSize(path string) int64 {
	vol := filepath.VolumeName(path) + `\`
	if v, ok := clusters.Load(vol); ok {
		return v.(int64)
	}
	var spc, bps, free, total uint32
	size := int64(4096)
	if p, err := windows.UTF16PtrFromString(vol); err == nil {
		r, _, _ := procGetDiskFreeSpace.Call(uintptr(unsafe.Pointer(p)),
			uintptr(unsafe.Pointer(&spc)), uintptr(unsafe.Pointer(&bps)),
			uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)))
		if r != 0 && spc*bps > 0 {
			size = int64(spc) * int64(bps)
		}
	}
	clusters.Store(vol, size)
	return size
}

// lstat reads an entry without following a reparse point. alloc is the
// size rounded up to the cluster size. Windows has no cheap way to read
// the device and inode, so the walk does not find hard links there.
func lstat(path string) (stat, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return stat{}, err
	}
	s := stat{mtime: fi.ModTime().Unix(), dir: fi.IsDir(), regular: fi.Mode().IsRegular(), nlink: 1}
	if size := fi.Size(); size > 0 {
		c := clusterSize(path)
		s.alloc = (size + c - 1) / c * c
	}
	return s, nil
}
