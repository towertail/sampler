package collect

import (
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/towertail/sampler/internal/schema"
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
