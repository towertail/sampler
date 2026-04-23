package collect

import (
	"os"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/towertail/sampler/pkg/schema"
)

// IsRoot reports whether the sampler is running with euid 0. Linux drops into
// the "can read every /proc/<pid>" regime here; macOS gets to enumerate
// other users' processes. We use this both to gate optional fields and to
// tell the Mac app the list is comprehensive.
func IsRoot() bool {
	return os.Geteuid() == 0
}

// Proc enumerates processes and returns the union top-N by CPU% and by RSS,
// deduped by PID. topN == 0 disables the cap (return everything). The
// caller should treat 20 as the default.
//
// CPU% requires a short self-sampling window so this function blocks for
// roughly `window` (same pattern as cpu.Percent). On macOS, non-root
// processes outside the user's own only return partial info via sysctl
// kinfo_proc — we surface whatever gopsutil gives us without trying to
// paper over the gap.
func Proc(window time.Duration, topN int) (schema.ProcList, []string) {
	var errs []string
	list := schema.ProcList{
		Root: IsRoot(),
		TopN: topN,
	}

	procs, err := process.Processes()
	if err != nil {
		errs = append(errs, "process.Processes: "+err.Error())
		list.Items = []schema.ProcSample{}
		return list, errs
	}
	list.Total = len(procs)

	// Hide the sampler from its own output. If we included ourselves, we'd
	// always rank at or near the top simply because we're doing CPU work
	// (walking /proc, computing deltas) during the very window we measure
	// — a self-referential artifact that's confusing and uninteresting.
	// Filter both by PID (our own) and by name (catches any stray sibling
	// `towertail-sampler` processes so a stale parent/child can't poke
	// through).
	selfPID := int32(os.Getpid())
	filtered := procs[:0]
	for _, p := range procs {
		if p.Pid == selfPID {
			continue
		}
		if name, err := p.Name(); err == nil && name == "towertail-sampler" {
			continue
		}
		filtered = append(filtered, p)
	}
	procs = filtered

	// First pass: snapshot CPU times. process.Percent(0, ...) returns the
	// % since process start — useless for our needs. We need the delta
	// across `window` so the reading matches what top(1) shows.
	type firstSnap struct {
		p    *process.Process
		prev float64
		t    time.Time
	}
	firsts := make([]firstSnap, 0, len(procs))
	for _, p := range procs {
		t, err := p.Times()
		if err != nil {
			// Process may have died between enumeration and sampling,
			// or be inaccessible without root (macOS). Skip silently —
			// a single noisy entry would dwarf the error list.
			continue
		}
		firsts = append(firsts, firstSnap{p: p, prev: t.Total(), t: time.Now()})
	}

	time.Sleep(window)

	items := make([]schema.ProcSample, 0, len(firsts))
	for _, f := range firsts {
		t, err := f.p.Times()
		if err != nil {
			continue
		}
		dt := time.Since(f.t).Seconds()
		if dt <= 0 {
			dt = window.Seconds()
		}
		busy := t.Total() - f.prev
		cpuPct := 0.0
		if busy > 0 && dt > 0 {
			cpuPct = (busy / dt) * 100.0
		}

		item := schema.ProcSample{
			PID:    f.p.Pid,
			CPUPct: cpuPct,
		}

		if name, err := f.p.Name(); err == nil {
			item.Name = name
		}
		if mi, err := f.p.MemoryInfo(); err == nil && mi != nil {
			item.RSS = int64(mi.RSS)
		}
		// Best-effort fields: missing or root-gated on some hosts. Don't
		// grow errors[] for each failure — there can be hundreds.
		if ppid, err := f.p.Ppid(); err == nil {
			item.PPID = ppid
		}
		if user, err := f.p.Username(); err == nil {
			item.User = user
		}
		if cmd, err := f.p.Cmdline(); err == nil {
			item.Cmd = cmd
		}
		if n, err := f.p.NumThreads(); err == nil {
			item.Threads = n
		}
		// NOTE: intentionally NOT calling p.Status() here. On macOS gopsutil
		// shells out to /bin/ps per PID for Status(), which spawns hundreds
		// of short-lived subprocesses per sample and makes the sampler flicker
		// all over Activity Monitor. We don't surface process state in the
		// UI, so pay the cost only if that changes.
		if ct, err := f.p.CreateTime(); err == nil && ct > 0 {
			item.StartTS = schema.FormatTS(time.UnixMilli(ct))
		}
		// Per-process disk I/O: requires /proc/<pid>/io read access on
		// Linux (owning-user or CAP_SYS_PTRACE). Unimplemented on Darwin
		// in gopsutil. When unavailable, leave fields nil so the Mac
		// app can distinguish "truly zero" from "no visibility."
		if io, err := f.p.IOCounters(); err == nil && io != nil {
			rb := int64(io.ReadBytes)
			wb := int64(io.WriteBytes)
			item.ReadBytes = &rb
			item.WriteBytes = &wb
		}

		items = append(items, item)
	}
	list.Visible = len(items)

	if topN > 0 && len(items) > topN {
		items = unionTopN(items, topN)
	}

	// Stable presentation order: CPU% desc, tiebreak RSS desc, then PID.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CPUPct != items[j].CPUPct {
			return items[i].CPUPct > items[j].CPUPct
		}
		if items[i].RSS != items[j].RSS {
			return items[i].RSS > items[j].RSS
		}
		return items[i].PID < items[j].PID
	})

	list.Items = items
	return list, errs
}

// unionTopN returns the union of the top N by CPU% and top N by RSS,
// deduped by PID. Size is between N and 2N.
func unionTopN(items []schema.ProcSample, n int) []schema.ProcSample {
	byCPU := make([]schema.ProcSample, len(items))
	byRSS := make([]schema.ProcSample, len(items))
	copy(byCPU, items)
	copy(byRSS, items)
	sort.SliceStable(byCPU, func(i, j int) bool { return byCPU[i].CPUPct > byCPU[j].CPUPct })
	sort.SliceStable(byRSS, func(i, j int) bool { return byRSS[i].RSS > byRSS[j].RSS })
	if n > len(byCPU) {
		n = len(byCPU)
	}
	seen := make(map[int32]struct{}, 2*n)
	out := make([]schema.ProcSample, 0, 2*n)
	for _, p := range byCPU[:n] {
		seen[p.PID] = struct{}{}
		out = append(out, p)
	}
	for _, p := range byRSS[:n] {
		if _, dup := seen[p.PID]; dup {
			continue
		}
		out = append(out, p)
	}
	return out
}
