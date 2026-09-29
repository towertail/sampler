package collect

import (
	"golang.org/x/sys/unix"

	"github.com/towertail/sampler/pkg/schema"
)

// sZomb is p_stat for a zombie in <sys/proc.h>.
const sZomb = 5

func healthPlatform(h *schema.HealthInfo) []string {
	var errs []string

	// One sysctl returns every kinfo_proc; no per-process calls.
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		errs = append(errs, "health.procs: "+err.Error())
	} else {
		entries := make([]procEntry, 0, len(procs))
		for i := range procs {
			p := &procs[i]
			entries = append(entries, procEntry{
				pid:    p.Proc.P_pid,
				ppid:   p.Eproc.Ppid,
				name:   unix.ByteSliceToString(p.Proc.P_comm[:]),
				zombie: p.Proc.P_stat == sZomb,
			})
		}
		applyProcEntries(h, entries)
		used := int64(len(procs))
		h.PidsUsed = &used
	}
	if v, err := unix.SysctlUint32("kern.maxproc"); err == nil {
		m := int64(v)
		h.PidsMax = &m
	}

	used, e1 := unix.SysctlUint32("kern.num_files")
	max, e2 := unix.SysctlUint32("kern.maxfiles")
	if e1 == nil && e2 == nil {
		u, m := int64(used), int64(max)
		h.FilesUsed = &u
		h.FilesMax = &m
	}

	// 1 = normal, 2 = warn, 4 = critical.
	if v, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		lvl := int(v)
		h.MemPressure = &lvl
	}
	return errs
}
