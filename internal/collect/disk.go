package collect

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/towertail/sampler/pkg/schema"
)

// fstypes we never want to report — synthetic, ephemeral, or not meaningful
// to the user looking at "disk usage."
var excludedFS = map[string]bool{
	"tmpfs":         true,
	"devfs":         true,
	"devtmpfs":      true,
	"overlay":       true,
	"squashfs":      true,
	"autofs":        true,
	"map auto_home": true,
	"nullfs":        true,
	"proc":          true,
	"sysfs":         true,
	"cgroup":        true,
	"cgroup2":       true,
	"debugfs":       true,
	"tracefs":       true,
	"mqueue":        true,
	"pstore":        true,
	"bpf":           true,
	"fusectl":       true,
	"configfs":      true,
	"securityfs":    true,
	"ramfs":         true,
	"rpc_pipefs":    true,
	"binfmt_misc":   true,
	"hugetlbfs":     true,
}

func hasOpt(opts []string, name string) bool {
	for _, o := range opts {
		if o == name {
			return true
		}
	}
	return false
}

func isReadOnly(opts []string) bool {
	return hasOpt(opts, "ro") || hasOpt(opts, "read-only")
}

const (
	statfsTimeout = 1 * time.Second
	diskBudget    = 5 * time.Second
)

var (
	errStatfsTimeout = errors.New("statfs timeout")
	errStatfsPending = errors.New("statfs pending")
)

type statfsFunc func(path string) (*disk.UsageStat, error)

// Paths with a statfs call that has not returned. A blocked call keeps its
// goroutine, so one call for each path is the most that can leak.
var (
	pendingMu    sync.Mutex
	pendingPaths = map[string]bool{}
)

func usageWithTimeout(path string, limit time.Duration, statfs statfsFunc) (*disk.UsageStat, error) {
	pendingMu.Lock()
	if pendingPaths[path] {
		pendingMu.Unlock()
		return nil, errStatfsPending
	}
	pendingPaths[path] = true
	pendingMu.Unlock()

	type result struct {
		u   *disk.UsageStat
		err error
	}
	ch := make(chan result, 1)
	go func() {
		u, err := statfs(path)
		pendingMu.Lock()
		delete(pendingPaths, path)
		pendingMu.Unlock()
		ch <- result{u, err}
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.u, r.err
	case <-timer.C:
		return nil, errStatfsTimeout
	}
}

func skipPartition(p disk.PartitionStat) bool {
	// Windows returns empty-mountpoint entries for removable drives
	// with no media. disk.Usage on an empty path errors on every poll.
	if p.Mountpoint == "" {
		return true
	}
	fs := strings.ToLower(p.Fstype)
	if excludedFS[fs] {
		return true
	}
	// OrbStack mounts one NFS share for each Docker volume and image.
	if strings.HasPrefix(p.Device, "OrbStack:") {
		return true
	}
	if fs == "nfs" && (hasOpt(p.Opts, "automounted") || hasOpt(p.Opts, "nobrowse")) {
		return true
	}
	// Skip nullfs/snap-like overlay paths that sneak through.
	if strings.HasPrefix(p.Mountpoint, "/snap/") {
		return true
	}
	// macOS has many synthetic firmlink volumes under /System/Volumes/.
	// /System/Volumes/Data reports the same stats as "/", so skip it too.
	if strings.HasPrefix(p.Mountpoint, "/System/Volumes/") {
		return true
	}
	// Read-only mounts are mostly DMGs and installer images. They report
	// 100% full by design and pin the "worst disk" metric. The macOS root
	// is read-only and sealed, so keep "/".
	if isReadOnly(p.Opts) && p.Mountpoint != "/" {
		return true
	}
	// DMG-style mounts can land under user temp dirs without the ro flag.
	return strings.Contains(p.Mountpoint, "/var/folders/")
}

func Disk() ([]schema.DiskSample, []string) {
	parts, err := partitions()
	if err != nil {
		return nil, []string{"disk.Partitions: " + err.Error()}
	}
	return collectDisks(parts, disk.Usage, diskBudget)
}

func collectDisks(parts []disk.PartitionStat, statfs statfsFunc, budget time.Duration) ([]schema.DiskSample, []string) {
	var errs []string
	out := make([]schema.DiskSample, 0, len(parts))
	seen := make(map[string]bool)
	deadline := time.Now().Add(budget)

	var mounts []disk.PartitionStat
	for _, p := range parts {
		if skipPartition(p) || seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		mounts = append(mounts, p)
	}

	for i, p := range mounts {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			errs = append(errs, fmt.Sprintf("disk: time limit %s reached, %d mounts not read", budget, len(mounts)-i))
			break
		}
		limit := min(statfsTimeout, remaining)
		u, err := usageWithTimeout(p.Mountpoint, limit, statfs)
		switch {
		case errors.Is(err, errStatfsTimeout):
			errs = append(errs, fmt.Sprintf("disk.Usage(%s): no result in %s, mount skipped", p.Mountpoint, limit))
			continue
		case errors.Is(err, errStatfsPending):
			errs = append(errs, "disk.Usage("+p.Mountpoint+"): earlier statfs still blocked, mount skipped")
			continue
		case err != nil:
			errs = append(errs, "disk.Usage("+p.Mountpoint+"): "+err.Error())
			continue
		}
		if u.Total == 0 {
			continue
		}
		iused, itotal := inodeCounts(u.InodesUsed, u.InodesFree, u.InodesTotal)
		out = append(out, schema.DiskSample{
			Mount:       p.Mountpoint,
			Fs:          p.Fstype,
			Used:        int64(u.Used),
			Total:       int64(u.Total),
			InodesUsed:  iused,
			InodesTotal: itotal,
		})
	}
	return out, errs
}

// inodeCounts returns zeros when the filesystem has no inode table.
// exFAT and FAT report iused=1 ifree=0, which reads as 100% use.
func inodeCounts(used, free, total uint64) (int64, int64) {
	if free == 0 && used <= 1 {
		return 0, 0
	}
	return int64(used), int64(total)
}

// isPartition reports whether `name` names a partition whose parent
// device is also present in `counters` (e.g. "sda1" when "sda" is in
// the map, "nvme0n1p1" when "nvme0n1" is). Partitions roll their I/O
// up into the whole-device counter, so including both would double
// the numbers. Orphan partitions (a partition with no visible parent,
// as happens inside some containers) are NOT filtered — otherwise we'd
// report zero.
func isPartition(name string, counters map[string]disk.IOCountersStat) bool {
	// Trim trailing digits (and optional 'p' before them for
	// nvme0n1p1 -> nvme0n1) to find a plausible parent name.
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i > 0 && name[i-1] == 'p' {
		i--
	}
	if i == 0 || i == len(name) {
		return false
	}
	parent := name[:i]
	_, ok := counters[parent]
	return ok
}

// sumDiskIO sums ReadBytes/WriteBytes across non-partition entries.
func sumDiskIO(counters map[string]disk.IOCountersStat) (read, write uint64) {
	for name, c := range counters {
		if isPartition(name, counters) {
			continue
		}
		read += c.ReadBytes
		write += c.WriteBytes
	}
	return
}

// DiskIO returns aggregate system-wide disk read/write counters plus a
// per-physical-device breakdown. Each device's ReadBps/WriteBps are the
// rate over `window`. Partitions are rolled up into their parent device
// to match the aggregate. No-op on platforms where gopsutil's
// disk.IOCounters returns an error (Darwin supports it via IOKit as of
// gopsutil v4; older platforms or containers without /proc/diskstats
// surface in errs).
func DiskIO(window time.Duration) (schema.DiskIOInfo, []string) {
	var errs []string
	var out schema.DiskIOInfo

	first, err := disk.IOCounters()
	if err != nil {
		errs = append(errs, "disk.IOCounters: "+err.Error())
		return out, errs
	}
	r1, w1 := sumDiskIO(first)
	t1 := time.Now()

	time.Sleep(window)

	second, err := disk.IOCounters()
	if err != nil {
		errs = append(errs, "disk.IOCounters: "+err.Error())
		out.ReadCum = int64(r1)
		out.WriteCum = int64(w1)
		return out, errs
	}
	r2, w2 := sumDiskIO(second)
	dt := time.Since(t1).Seconds()

	out.ReadCum = int64(r2)
	out.WriteCum = int64(w2)
	if dt > 0 {
		if r2 >= r1 {
			out.ReadBps = int64(float64(r2-r1) / dt)
		}
		if w2 >= w1 {
			out.WriteBps = int64(float64(w2-w1) / dt)
		}
	}

	// Per-device breakdown. We iterate the second snapshot (current
	// counter state) and look each device up in the first snapshot to
	// compute the delta. Devices that appeared mid-window keep their
	// full ReadCum/WriteCum and report zero rate until the next tick.
	devices := make([]schema.DiskIODeviceInfo, 0, len(second))
	for name, c2 := range second {
		if isPartition(name, second) {
			continue
		}
		dev := schema.DiskIODeviceInfo{
			Name:     name,
			ReadCum:  int64(c2.ReadBytes),
			WriteCum: int64(c2.WriteBytes),
		}
		if c1, ok := first[name]; ok && dt > 0 {
			if c2.ReadBytes >= c1.ReadBytes {
				dev.ReadBps = int64(float64(c2.ReadBytes-c1.ReadBytes) / dt)
			}
			if c2.WriteBytes >= c1.WriteBytes {
				dev.WriteBps = int64(float64(c2.WriteBytes-c1.WriteBytes) / dt)
			}
		}
		devices = append(devices, dev)
	}
	// Stable ordering by name — helps the Mac-side picker keep its
	// selection row stable across ticks.
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	out.Devices = devices
	return out, errs
}
