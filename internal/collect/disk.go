package collect

import (
	"sort"
	"strings"
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

func isReadOnly(opts []string) bool {
	for _, o := range opts {
		if o == "ro" || o == "read-only" {
			return true
		}
	}
	return false
}

func Disk() ([]schema.DiskSample, []string) {
	var errs []string
	parts, err := disk.Partitions(false)
	if err != nil {
		errs = append(errs, "disk.Partitions: "+err.Error())
		return nil, errs
	}

	out := make([]schema.DiskSample, 0, len(parts))
	seen := make(map[string]bool)

	for _, p := range parts {
		fs := strings.ToLower(p.Fstype)
		if excludedFS[fs] {
			continue
		}
		// Skip nullfs/snap-like overlay paths that sneak through.
		if strings.HasPrefix(p.Mountpoint, "/snap/") {
			continue
		}
		// macOS has many synthetic firmlink volumes under /System/Volumes/.
		// Only /System/Volumes/Data is a real user-facing disk on APFS,
		// but it reports identical stats to "/" so we skip it too —
		// the user only cares about one row for the root volume.
		if strings.HasPrefix(p.Mountpoint, "/System/Volumes/") {
			continue
		}
		// Read-only mounts are almost always app-bundle DMGs, installer
		// images, or snap-style overlays that report 100% full by design
		// (they're sized exactly to their contents). Including them makes
		// the "worst disk" metric permanently pinned at 100% whenever any
		// such image is mounted. The actual root filesystem on macOS is
		// also `ro` + `sealed`, but we already pick up the writable
		// /System/Volumes/Data equivalent via its own entry — except we
		// skip /System/Volumes/* above, so allow "/" through even if `ro`.
		if isReadOnly(p.Opts) && p.Mountpoint != "/" {
			continue
		}
		// DMG-style ephemeral mounts sometimes land under user temp dirs
		// rather than /Volumes; skip those explicitly in case the ro flag
		// doesn't surface on some FS drivers.
		if strings.Contains(p.Mountpoint, "/var/folders/") {
			continue
		}
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true

		u, err := disk.Usage(p.Mountpoint)
		if err != nil {
			errs = append(errs, "disk.Usage("+p.Mountpoint+"): "+err.Error())
			continue
		}
		if u.Total == 0 {
			continue
		}
		out = append(out, schema.DiskSample{
			Mount: p.Mountpoint,
			Fs:    p.Fstype,
			Used:  int64(u.Used),
			Total: int64(u.Total),
		})
	}
	return out, errs
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
