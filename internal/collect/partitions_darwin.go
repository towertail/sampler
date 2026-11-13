//go:build darwin

package collect

import (
	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"
)

var mountFlagOpts = []struct {
	flag uint32
	opt  string
}{
	{unix.MNT_SYNCHRONOUS, "sync"},
	{unix.MNT_NOEXEC, "noexec"},
	{unix.MNT_NOSUID, "nosuid"},
	{unix.MNT_UNION, "union"},
	{unix.MNT_ASYNC, "async"},
	{unix.MNT_DONTBROWSE, "nobrowse"},
	{unix.MNT_AUTOMOUNTED, "automounted"},
	{unix.MNT_JOURNALED, "journaled"},
	{unix.MNT_MULTILABEL, "multilabel"},
	{unix.MNT_NOATIME, "noatime"},
	{unix.MNT_NODEV, "nodev"},
	{unix.MNT_LOCAL, "local"},
	{unix.MNT_CPROTECT, "protect"},
}

// partitions lists mounts like gopsutil, but with MNT_NOWAIT. MNT_WAIT
// refreshes every NFS mount and takes about 50 s with 260 OrbStack mounts.
func partitions() ([]disk.PartitionStat, error) {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	fs := make([]unix.Statfs_t, count)
	count, err = unix.Getfsstat(fs, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	// The count can shrink between the two calls.
	fs = fs[:count]

	ret := make([]disk.PartitionStat, 0, len(fs))
	for i := range fs {
		stat := &fs[i]
		opts := []string{"rw"}
		if stat.Flags&unix.MNT_RDONLY != 0 {
			opts = []string{"ro"}
		}
		for _, f := range mountFlagOpts {
			if stat.Flags&f.flag != 0 {
				opts = append(opts, f.opt)
			}
		}
		ret = append(ret, disk.PartitionStat{
			Device:     unix.ByteSliceToString(stat.Mntfromname[:]),
			Mountpoint: unix.ByteSliceToString(stat.Mntonname[:]),
			Fstype:     unix.ByteSliceToString(stat.Fstypename[:]),
			Opts:       opts,
		})
	}
	return ret, nil
}
