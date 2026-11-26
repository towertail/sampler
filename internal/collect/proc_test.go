package collect

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/towertail/sampler/pkg/schema"
)

func TestProcLive(t *testing.T) {
	list, errs := Proc(ProcOptions{Window: 50 * time.Millisecond, TopN: 5})
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if list.Total <= 0 {
		t.Fatalf("expected > 0 total processes, got %d", list.Total)
	}
	if len(list.Items) == 0 {
		t.Fatal("expected at least one item in process list")
	}
	named := false
	for _, p := range list.Items {
		if p.PID > 0 && p.Name != "" {
			named = true
			break
		}
	}
	if !named {
		t.Error("expected at least one proc with PID and Name populated")
	}
	if list.TopN != 5 {
		t.Errorf("TopN: got %d want 5", list.TopN)
	}
	// Top 5 by CPU, top 5 by RSS and top DefaultProcTopIO by I/O.
	if limit := 2*5 + DefaultProcTopIO; len(list.Items) > limit {
		t.Errorf("expected at most %d items, got %d", limit, len(list.Items))
	}
	if want := min(5, list.Visible); len(list.Items) < want {
		t.Errorf("expected at least %d items, got %d", want, len(list.Items))
	}
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i-1].CPUPct < list.Items[i].CPUPct {
			t.Errorf("items not sorted by CPU desc at %d: %v < %v",
				i, list.Items[i-1].CPUPct, list.Items[i].CPUPct)
		}
	}
}

func TestUnionTopNDedups(t *testing.T) {
	items := []schema.ProcSample{
		{PID: 1, CPUPct: 90, RSS: 10},   // top by CPU, not by RSS
		{PID: 2, CPUPct: 80, RSS: 100},  // top by both (dedup target)
		{PID: 3, CPUPct: 10, RSS: 1000}, // top by RSS, not by CPU
		{PID: 4, CPUPct: 5, RSS: 5},     // neither
	}
	out := unionTopN(items, 2, 0)
	// top CPU: [1, 2]; top RSS: [3, 2] → union [1,2,3], size 3.
	if len(out) != 3 {
		t.Fatalf("expected 3 unique items, got %d: %+v", len(out), out)
	}
	seen := map[int32]bool{}
	for _, p := range out {
		if seen[p.PID] {
			t.Errorf("duplicate PID %d in union", p.PID)
		}
		seen[p.PID] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Errorf("expected PIDs 1,2,3 in union, got %+v", out)
	}
	if seen[4] {
		t.Error("PID 4 shouldn't appear (neither top CPU nor top RSS)")
	}
}

func TestProcSkipsScanAboveMax(t *testing.T) {
	list, _ := Proc(ProcOptions{Window: 50 * time.Millisecond, TopN: 20, ScanMax: 1})
	if !list.Skipped {
		t.Fatalf("expected Skipped with scanMax=1, total=%d", list.Total)
	}
	if list.Total < 2 {
		t.Errorf("Total should still be counted, got %d", list.Total)
	}
	if len(list.Items) != 0 {
		t.Errorf("Items should be empty when skipped, got %d", len(list.Items))
	}
}

func ptr(v int64) *int64 { return &v }

func TestUnionTopNAddsTopIO(t *testing.T) {
	items := []schema.ProcSample{
		{PID: 1, CPUPct: 90, RSS: 100},
		{PID: 2, CPUPct: 80, RSS: 90},
		{PID: 3, CPUPct: 1, RSS: 1, ReadBps: ptr(500), WriteBps: ptr(500)},
		{PID: 4, CPUPct: 1, RSS: 1, ReadBps: ptr(100)},
		{PID: 5, CPUPct: 1, RSS: 1, WriteBps: ptr(10)},
	}
	out := unionTopN(items, 1, 2)
	got := map[int32]bool{}
	for _, p := range out {
		got[p.PID] = true
	}
	// CPU: 1. RSS: 1. I/O: 3 and 4.
	if len(out) != 3 || !got[1] || !got[3] || !got[4] {
		t.Fatalf("want PIDs 1,3,4, got %+v", out)
	}
}

func TestUnionTopNSkipsRowsWithoutIO(t *testing.T) {
	items := []schema.ProcSample{
		{PID: 1, CPUPct: 90, RSS: 100},
		{PID: 2, CPUPct: 80, RSS: 90},
		{PID: 3, ReadBps: ptr(0), WriteBps: ptr(0)},
		{PID: 4},
	}
	out := unionTopN(items, 1, 10)
	if len(out) != 1 || out[0].PID != 1 {
		t.Fatalf("want only PID 1, got %+v", out)
	}
}

func TestUnionTopNDedupsIO(t *testing.T) {
	items := []schema.ProcSample{
		{PID: 1, CPUPct: 90, RSS: 100, ReadBps: ptr(1000)},
		{PID: 2, CPUPct: 1, RSS: 1},
	}
	out := unionTopN(items, 1, 5)
	if len(out) != 1 {
		t.Fatalf("want 1 item, got %+v", out)
	}
}

// fakeProc is a procInfo that counts the calls of each method.
type fakeProc struct {
	pid      int32
	name     string
	ppid     int32
	cwd      string
	cpu      []float64 // total CPU seconds for snapshot 1, 2
	reads    []uint64  // read bytes for snapshot 1, 2; a missing entry is an error
	rss      uint64
	calls    map[string]int
	ioErrAt0 bool
}

func (f *fakeProc) hit(m string) { f.calls[m]++ }

func (f *fakeProc) PID() int32 { return f.pid }
func (f *fakeProc) Name() (string, error) {
	f.hit("Name")
	return f.name, nil
}
func (f *fakeProc) Times() (*cpu.TimesStat, error) {
	f.hit("Times")
	i := min(f.calls["Times"]-1, len(f.cpu)-1)
	return &cpu.TimesStat{User: f.cpu[i]}, nil
}
func (f *fakeProc) IOCounters() (*process.IOCountersStat, error) {
	f.hit("IOCounters")
	i := f.calls["IOCounters"] - 1
	if i >= len(f.reads) || (i == 0 && f.ioErrAt0) {
		return nil, errors.New("no io")
	}
	return &process.IOCountersStat{ReadBytes: f.reads[i], WriteBytes: f.reads[i]}, nil
}
func (f *fakeProc) MemoryInfo() (*process.MemoryInfoStat, error) {
	f.hit("MemoryInfo")
	return &process.MemoryInfoStat{RSS: f.rss}, nil
}
func (f *fakeProc) Ppid() (int32, error) { f.hit("Ppid"); return f.ppid, nil }
func (f *fakeProc) Username() (string, error) {
	f.hit("Username")
	return "u", nil
}
func (f *fakeProc) Cmdline() (string, error)   { f.hit("Cmdline"); return "cmd", nil }
func (f *fakeProc) NumThreads() (int32, error) { f.hit("NumThreads"); return 3, nil }
func (f *fakeProc) CreateTime() (int64, error) { f.hit("CreateTime"); return 1000, nil }
func (f *fakeProc) Cwd() (string, error)       { f.hit("Cwd"); return f.cwd, nil }

func newFakeProc(pid int32, name string) *fakeProc {
	return &fakeProc{pid: pid, name: name, calls: map[string]int{}, cpu: []float64{0, 0}, cwd: "/work"}
}

// fakeScan builds a scan on a fake clock. The sleep advances the clock.
func fakeScan(opts ProcOptions, procs ...*fakeProc) procScan {
	clock := time.Unix(1000, 0)
	infos := make([]procInfo, len(procs))
	for i, p := range procs {
		infos[i] = p
	}
	return procScan{
		opts:    opts,
		list:    func() ([]procInfo, error) { return infos, nil },
		now:     func() time.Time { return clock },
		sleep:   func(d time.Duration) { clock = clock.Add(d) },
		repos:   &repoFinder{stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, budget: time.Second, cache: map[string]string{}},
		selfPID: -1,
	}
}

func TestProcScanDetailOnlyForKeptRows(t *testing.T) {
	var procs []*fakeProc
	for i := int32(1); i <= 30; i++ {
		p := newFakeProc(i, "p")
		p.cpu = []float64{0, float64(i) * 0.01}
		p.rss = 1
		p.reads = []uint64{0, 0}
		procs = append(procs, p)
	}
	// PIDs 29 and 30 have the top CPU%. PIDs 1 and 2 have the top RSS.
	procs[0].rss = 1000
	list, _ := fakeScan(ProcOptions{Window: 200 * time.Millisecond, TopN: 2}, procs...).run()
	if list.Visible != 30 || len(list.Items) != 4 {
		t.Fatalf("visible = %d, items = %d", list.Visible, len(list.Items))
	}
	kept := map[int32]bool{}
	for _, it := range list.Items {
		kept[it.PID] = true
	}
	for _, p := range procs {
		detail := p.calls["Ppid"] + p.calls["Username"] + p.calls["Cmdline"] +
			p.calls["NumThreads"] + p.calls["CreateTime"] + p.calls["Cwd"]
		if kept[p.pid] && detail != 6 {
			t.Errorf("pid %d kept: detail calls = %d, want 6", p.pid, detail)
		}
		if !kept[p.pid] && detail != 0 {
			t.Errorf("pid %d dropped: detail calls = %d, want 0", p.pid, detail)
		}
		if p.calls["IOCounters"] != 2 {
			t.Errorf("pid %d: IOCounters calls = %d, want 2", p.pid, p.calls["IOCounters"])
		}
	}
}

func TestProcScanReadBpsFromTwoSnapshots(t *testing.T) {
	p := newFakeProc(1, "a")
	p.reads = []uint64{1000, 3000}
	bad := newFakeProc(2, "b")
	bad.reads = []uint64{0, 3000}
	bad.ioErrAt0 = true
	list, _ := fakeScan(ProcOptions{Window: 200 * time.Millisecond}, p, bad).run()
	got := map[int32]schema.ProcSample{}
	for _, it := range list.Items {
		got[it.PID] = it
	}
	a := got[1]
	if a.ReadBps == nil || *a.ReadBps != 10000 || *a.WriteBps != 10000 {
		t.Errorf("rate = %v, want 10000", a.ReadBps)
	}
	if a.ReadBytes == nil || *a.ReadBytes != 3000 {
		t.Errorf("ReadBytes = %v, want 3000", a.ReadBytes)
	}
	b := got[2]
	if b.ReadBps != nil || b.WriteBps != nil {
		t.Errorf("error at t0: want nil rate, got %v %v", b.ReadBps, b.WriteBps)
	}
	if b.ReadBytes == nil || *b.ReadBytes != 3000 {
		t.Errorf("error at t0: ReadBytes = %v, want 3000", b.ReadBytes)
	}
}

func TestProcScanParentFromNames(t *testing.T) {
	parent := newFakeProc(10, "pm2")
	child := newFakeProc(20, "node")
	child.ppid = 10
	orphan := newFakeProc(30, "lone")
	orphan.ppid = 999
	list, _ := fakeScan(ProcOptions{Window: time.Millisecond}, parent, child, orphan).run()
	got := map[int32]schema.ProcSample{}
	for _, it := range list.Items {
		got[it.PID] = it
	}
	if got[20].Parent != "pm2" || got[20].PPID != 10 {
		t.Errorf("child = %+v", got[20])
	}
	if got[30].Parent != "" || got[30].PPID != 999 {
		t.Errorf("orphan = %+v", got[30])
	}
}

func TestProcScanNoCwd(t *testing.T) {
	p := newFakeProc(1, "a")
	s := fakeScan(ProcOptions{Window: time.Millisecond, NoCwd: true}, p)
	s.repos = &repoFinder{stat: func(string) (os.FileInfo, error) {
		t.Error("repo lookup with NoCwd")
		return nil, os.ErrNotExist
	}, budget: time.Second, cache: map[string]string{}}
	list, _ := s.run()
	if p.calls["Cwd"] != 0 {
		t.Errorf("Cwd calls = %d, want 0", p.calls["Cwd"])
	}
	if it := list.Items[0]; it.Cwd != "" || it.Repo != "" {
		t.Errorf("item = %+v", it)
	}
}

func TestProcScanHidesSelf(t *testing.T) {
	self := newFakeProc(5, "towertail-sampler.exe")
	other := newFakeProc(6, "x")
	s := fakeScan(ProcOptions{Window: time.Millisecond}, self, other)
	list, _ := s.run()
	if len(list.Items) != 1 || list.Items[0].PID != 6 {
		t.Fatalf("items = %+v", list.Items)
	}
}

func TestProcLiveChildFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no cwd read on Windows")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "2")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	list, _ := Proc(ProcOptions{Window: 50 * time.Millisecond})
	for _, it := range list.Items {
		if it.PID != int32(cmd.Process.Pid) {
			continue
		}
		if it.Cwd == "" {
			t.Skip("no cwd visibility on this host")
		}
		if it.Cwd != dir || it.Repo != dir {
			t.Errorf("cwd = %q, repo = %q, want %q", it.Cwd, it.Repo, dir)
		}
		self, _ := os.Executable()
		if it.Parent != filepath.Base(self) {
			t.Errorf("parent = %q, want %q", it.Parent, filepath.Base(self))
		}
		return
	}
	t.Fatal("child process not in the list")
}
