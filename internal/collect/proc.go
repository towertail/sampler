package collect

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/towertail/sampler/pkg/schema"
)

// IsRoot reports whether the sampler is running with the host's
// privileged regime — euid 0 on Unix, an elevated token on Windows.
// Linux drops into the "can read every /proc/<pid>" regime here; macOS
// gets to enumerate other users' processes; Windows can query
// cross-user GetProcessIoCounters and WMI classes otherwise gated.
// We use this both to gate optional fields and to tell the client the
// process list is comprehensive.
func IsRoot() bool {
	return isElevated()
}

// DefaultProcScanMax is the process count above which Proc skips the
// per-process scan. The scan costs about 0.6 ms per process, so 10k
// processes take about 6 s, close to the client's 10 s timeout.
const DefaultProcScanMax = 10000

// DefaultProcTopIO is the number of rows that the top by I/O rate adds
// to the top N by CPU% and by RSS.
const DefaultProcTopIO = 10

// ProcOptions are the inputs of Proc.
type ProcOptions struct {
	// Window is the time between the two snapshots.
	Window time.Duration
	// TopN caps the list. 0 returns every process.
	TopN int
	// ScanMax is the process count above which Proc skips the scan.
	// 0 or less disables the limit.
	ScanMax int
	// NoCwd skips the working folder and the repo.
	NoCwd bool
}

// procInfo is the part of a process that the scan reads.
type procInfo interface {
	PID() int32
	Name() (string, error)
	Times() (*cpu.TimesStat, error)
	IOCounters() (*process.IOCountersStat, error)
	MemoryInfo() (*process.MemoryInfoStat, error)
	Ppid() (int32, error)
	Username() (string, error)
	Cmdline() (string, error)
	NumThreads() (int32, error)
	CreateTime() (int64, error)
	Cwd() (string, error)
}

type gopsProc struct{ *process.Process }

func (p gopsProc) PID() int32 { return p.Pid }

// Proc enumerates processes and returns the union of the top N by CPU%,
// the top N by RSS and the top DefaultProcTopIO by I/O rate, deduped by
// PID. The caller should treat 20 as the default TopN. When the host has
// more than ScanMax processes, Proc returns only the count with Skipped
// set.
//
// CPU% and the I/O rate need two snapshots, so Proc blocks for roughly
// Window (same pattern as cpu.Percent). The two full passes read only
// what ranks the rows. The detail fields come after the cut, for the kept
// rows only. On macOS, non-root processes outside the user's own only
// return partial info via sysctl kinfo_proc.
func Proc(o ProcOptions) (schema.ProcList, []string) {
	return procScan{
		opts: o,
		list: func() ([]procInfo, error) {
			ps, err := process.Processes()
			out := make([]procInfo, len(ps))
			for i, p := range ps {
				out[i] = gopsProc{p}
			}
			return out, err
		},
		now:     time.Now,
		sleep:   time.Sleep,
		repos:   newRepoFinder(repoBudget),
		selfPID: int32(os.Getpid()),
	}.run()
}

type procScan struct {
	opts    ProcOptions
	list    func() ([]procInfo, error)
	now     func() time.Time
	sleep   func(time.Duration)
	repos   *repoFinder
	selfPID int32
}

// procSnap is the first snapshot of one process.
type procSnap struct {
	p     procInfo
	cpu   float64
	at    time.Time
	r, w  uint64
	hasIO bool
}

func (s procScan) run() (schema.ProcList, []string) {
	var errs []string
	list := schema.ProcList{
		Root: IsRoot(),
		TopN: s.opts.TopN,
	}

	procs, err := s.list()
	if err != nil {
		errs = append(errs, "process.Processes: "+err.Error())
		list.Items = []schema.ProcSample{}
		return list, errs
	}
	list.Total = len(procs)
	if s.opts.ScanMax > 0 && list.Total > s.opts.ScanMax {
		list.Skipped = true
		list.Items = []schema.ProcSample{}
		return list, errs
	}

	// First pass: snapshot CPU times and I/O counters. The sampler hides
	// itself: it would always rank near the top for the work it does in
	// the window. The filter matches the PID and the name, so a stray
	// sibling `towertail-sampler` does not show either.
	names := make(map[int32]string, len(procs))
	firsts := make([]procSnap, 0, len(procs))
	for _, p := range procs {
		name, nameErr := p.Name()
		if nameErr == nil {
			names[p.PID()] = name
		}
		if p.PID() == s.selfPID || strings.TrimSuffix(name, ".exe") == "towertail-sampler" {
			continue
		}
		t, err := p.Times()
		if err != nil {
			// The process may have died, or may be closed to us
			// (macOS). Skip it: one error line each would flood errors[].
			continue
		}
		f := procSnap{p: p, cpu: t.Total(), at: s.now()}
		if io, err := p.IOCounters(); err == nil && io != nil {
			f.r, f.w, f.hasIO = io.ReadBytes, io.WriteBytes, true
		}
		firsts = append(firsts, f)
	}

	s.sleep(s.opts.Window)

	items := make([]schema.ProcSample, 0, len(firsts))
	byPID := make(map[int32]procInfo, len(firsts))
	for _, f := range firsts {
		t, err := f.p.Times()
		if err != nil {
			continue
		}
		io, ioErr := f.p.IOCounters()
		dt := s.now().Sub(f.at).Seconds()
		if dt <= 0 {
			dt = s.opts.Window.Seconds()
		}
		busy := t.Total() - f.cpu
		cpuPct := 0.0
		if busy > 0 && dt > 0 {
			cpuPct = (busy / dt) * 100.0
		}

		item := schema.ProcSample{
			PID:    f.p.PID(),
			Name:   names[f.p.PID()],
			CPUPct: cpuPct,
		}
		if mi, err := f.p.MemoryInfo(); err == nil && mi != nil {
			item.RSS = int64(mi.RSS)
		}
		// Per-process disk I/O: requires /proc/<pid>/io read access on
		// Linux (owning-user or CAP_SYS_PTRACE). Unimplemented on Darwin
		// in gopsutil. When unavailable, leave fields nil so the Mac
		// app can distinguish "truly zero" from "no visibility."
		if ioErr == nil && io != nil {
			rb := int64(io.ReadBytes)
			wb := int64(io.WriteBytes)
			item.ReadBytes = &rb
			item.WriteBytes = &wb
			if f.hasIO && io.ReadBytes >= f.r && io.WriteBytes >= f.w {
				rbps := int64(float64(io.ReadBytes-f.r) / dt)
				wbps := int64(float64(io.WriteBytes-f.w) / dt)
				item.ReadBps = &rbps
				item.WriteBps = &wbps
			}
		}

		items = append(items, item)
		byPID[item.PID] = f.p
	}
	list.Visible = len(items)

	if s.opts.TopN > 0 && len(items) > s.opts.TopN {
		items = unionTopN(items, s.opts.TopN, DefaultProcTopIO)
	}

	// Detail pass: best-effort fields for the kept rows only. Do not grow
	// errors[] for each failure: there can be hundreds.
	//
	// NOTE: intentionally NOT calling p.Status() here. On macOS gopsutil
	// shells out to /bin/ps per PID for Status(), which spawns hundreds
	// of short-lived subprocesses per sample and makes the sampler flicker
	// all over Activity Monitor. We don't surface process state in the
	// UI, so pay the cost only if that changes.
	var cwds []string
	for i := range items {
		it := &items[i]
		p := byPID[it.PID]
		if ppid, err := p.Ppid(); err == nil {
			it.PPID = ppid
			it.Parent = names[ppid]
		}
		if user, err := p.Username(); err == nil {
			it.User = user
		}
		if cmd, err := p.Cmdline(); err == nil {
			it.Cmd = cmd
		}
		if n, err := p.NumThreads(); err == nil {
			it.Threads = n
		}
		if ct, err := p.CreateTime(); err == nil && ct > 0 {
			it.StartTS = schema.FormatTS(time.UnixMilli(ct))
		}
		if s.opts.NoCwd {
			continue
		}
		if cwd, err := p.Cwd(); err == nil {
			it.Cwd = cwd
			cwds = append(cwds, cwd)
		}
	}
	if !s.opts.NoCwd {
		roots := s.repos.Roots(cwds)
		for i := range items {
			items[i].Repo = roots[items[i].Cwd]
		}
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

// unionTopN returns the union of the top n by CPU%, the top n by RSS and
// the top ioN by I/O rate, deduped by PID. A row without an I/O rate, or
// with a zero rate, does not count for the I/O ranking. Size is between
// n and 2n+ioN.
func unionTopN(items []schema.ProcSample, n, ioN int) []schema.ProcSample {
	byCPU := make([]schema.ProcSample, len(items))
	byRSS := make([]schema.ProcSample, len(items))
	copy(byCPU, items)
	copy(byRSS, items)
	sort.SliceStable(byCPU, func(i, j int) bool { return byCPU[i].CPUPct > byCPU[j].CPUPct })
	sort.SliceStable(byRSS, func(i, j int) bool { return byRSS[i].RSS > byRSS[j].RSS })
	if n > len(byCPU) {
		n = len(byCPU)
	}
	seen := make(map[int32]struct{}, 2*n+ioN)
	out := make([]schema.ProcSample, 0, 2*n+ioN)
	add := func(p schema.ProcSample) {
		if _, dup := seen[p.PID]; dup {
			return
		}
		seen[p.PID] = struct{}{}
		out = append(out, p)
	}
	for _, p := range byCPU[:n] {
		add(p)
	}
	for _, p := range byRSS[:n] {
		add(p)
	}
	if ioN > 0 {
		byIO := make([]schema.ProcSample, 0, len(items))
		for _, p := range items {
			if ioRate(p) > 0 {
				byIO = append(byIO, p)
			}
		}
		sort.SliceStable(byIO, func(i, j int) bool { return ioRate(byIO[i]) > ioRate(byIO[j]) })
		for _, p := range byIO[:min(ioN, len(byIO))] {
			add(p)
		}
	}
	return out
}

// ioRate is ReadBps+WriteBps. A missing rate counts as 0.
func ioRate(p schema.ProcSample) int64 {
	var sum int64
	if p.ReadBps != nil {
		sum += *p.ReadBps
	}
	if p.WriteBps != nil {
		sum += *p.WriteBps
	}
	return sum
}
