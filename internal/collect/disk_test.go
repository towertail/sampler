package collect

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

func TestDiskFiltering(t *testing.T) {
	ds, errs := Disk()
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if len(ds) == 0 {
		t.Fatalf("expected at least one disk on dev machine")
	}
	for _, d := range ds {
		fs := strings.ToLower(d.Fs)
		if excludedFS[fs] {
			t.Errorf("excluded fs leaked into output: %+v", d)
		}
		if d.Total <= 0 {
			t.Errorf("disk total should be > 0: %+v", d)
		}
		if d.Used < 0 || d.Used > d.Total {
			t.Errorf("disk used out of range: %+v", d)
		}
	}
}

func TestSumDiskIOFiltersPartitions(t *testing.T) {
	// sda + sda1 + sda2 + nvme0n1 + nvme0n1p1 — partitions should be
	// skipped so we don't double-count their parents' counters.
	counters := map[string]disk.IOCountersStat{
		"sda":       {ReadBytes: 1000, WriteBytes: 500},
		"sda1":      {ReadBytes: 600, WriteBytes: 300},
		"sda2":      {ReadBytes: 400, WriteBytes: 200},
		"nvme0n1":   {ReadBytes: 2000, WriteBytes: 800},
		"nvme0n1p1": {ReadBytes: 2000, WriteBytes: 800},
	}
	r, w := sumDiskIO(counters)
	if r != 3000 {
		t.Errorf("read: want 3000 (sda+nvme0n1 only), got %d", r)
	}
	if w != 1300 {
		t.Errorf("write: want 1300, got %d", w)
	}
}

func TestSumDiskIOOrphanPartitions(t *testing.T) {
	// A container-ish case: only "sda1" is visible (no "sda" parent).
	// Don't filter it out — otherwise we'd report zero.
	counters := map[string]disk.IOCountersStat{
		"sda1": {ReadBytes: 100, WriteBytes: 50},
	}
	r, w := sumDiskIO(counters)
	if r != 100 || w != 50 {
		t.Errorf("orphan partition: want 100/50, got %d/%d", r, w)
	}
}

func TestDiskIOCumulativesMonotonic(t *testing.T) {
	io, errs := DiskIO(50 * time.Millisecond)
	if len(errs) > 0 {
		t.Skipf("DiskIO unavailable on this host: %v", errs)
	}
	if io.ReadCum < 0 || io.WriteCum < 0 {
		t.Errorf("cumulative counters should be non-negative: %+v", io)
	}
	if io.ReadBps < 0 || io.WriteBps < 0 {
		t.Errorf("bps should be non-negative: %+v", io)
	}
}

func TestInodeCountsDropsPlaceholders(t *testing.T) {
	cases := []struct {
		used, free, total uint64
		wantUsed, wantTot int64
	}{
		{1, 0, 1, 0, 0},         // exFAT / FAT on macOS
		{0, 0, 0, 0, 0},         // no inode data
		{500, 0, 500, 500, 500}, // really exhausted
		{90, 10, 100, 90, 100},  // normal
	}
	for _, c := range cases {
		u, tot := inodeCounts(c.used, c.free, c.total)
		if u != c.wantUsed || tot != c.wantTot {
			t.Errorf("inodeCounts(%d,%d,%d) = %d,%d; want %d,%d",
				c.used, c.free, c.total, u, tot, c.wantUsed, c.wantTot)
		}
	}
}

func TestSkipPartition(t *testing.T) {
	cases := []struct {
		name string
		p    disk.PartitionStat
		skip bool
	}{
		{"orbstack nfs automounted", disk.PartitionStat{Device: "OrbStack://docker/volumes/x", Mountpoint: "/Users/u/OrbStack/docker/volumes/x", Fstype: "nfs", Opts: []string{"rw", "automounted"}}, true},
		{"orbstack nfs nobrowse", disk.PartitionStat{Device: "OrbStack://docker/volumes/x", Mountpoint: "/Users/u/OrbStack/docker/volumes/x", Fstype: "nfs", Opts: []string{"rw", "nobrowse"}}, true},
		{"orbstack root", disk.PartitionStat{Device: "OrbStack:/", Mountpoint: "/Users/u/OrbStack", Fstype: "nfs", Opts: []string{"rw", "nodev", "nosuid"}}, true},
		{"orbstack virtiofs", disk.PartitionStat{Device: "OrbStack://mac", Mountpoint: "/mnt/mac", Fstype: "virtiofs", Opts: []string{"rw"}}, true},
		{"nfs automounted", disk.PartitionStat{Device: "nas:/export", Mountpoint: "/net/nas", Fstype: "nfs", Opts: []string{"rw", "automounted"}}, true},
		{"NFS nobrowse", disk.PartitionStat{Device: "nas:/export", Mountpoint: "/net/nas", Fstype: "NFS", Opts: []string{"rw", "nobrowse"}}, true},
		{"nfs rw", disk.PartitionStat{Device: "nas:/export", Mountpoint: "/Volumes/nas", Fstype: "nfs", Opts: []string{"rw"}}, false},
		{"apfs nobrowse", disk.PartitionStat{Device: "/dev/disk3s5", Mountpoint: "/Volumes/Backup", Fstype: "apfs", Opts: []string{"rw", "nobrowse"}}, false},
		{"apfs ro root", disk.PartitionStat{Device: "/dev/disk3s1s1", Mountpoint: "/", Fstype: "apfs", Opts: []string{"ro"}}, false},
		{"tmpfs", disk.PartitionStat{Device: "tmpfs", Mountpoint: "/run", Fstype: "tmpfs", Opts: []string{"rw"}}, true},
		{"system data", disk.PartitionStat{Device: "/dev/disk3s5", Mountpoint: "/System/Volumes/Data", Fstype: "apfs", Opts: []string{"rw"}}, true},
	}
	for _, c := range cases {
		if got := skipPartition(c.p); got != c.skip {
			t.Errorf("%s: skip = %v, want %v", c.name, got, c.skip)
		}
	}
}

func okStatfs(string) (*disk.UsageStat, error) {
	return &disk.UsageStat{Total: 100, Used: 40, InodesTotal: 10, InodesUsed: 2, InodesFree: 8}, nil
}

func TestUsageWithTimeoutReturnsResult(t *testing.T) {
	u, err := usageWithTimeout("/ok", time.Second, okStatfs)
	if err != nil || u.Total != 100 {
		t.Fatalf("got %v, %v", u, err)
	}
}

func TestUsageWithTimeoutTimesOut(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	_, err := usageWithTimeout("/hang", 50*time.Millisecond, func(string) (*disk.UsageStat, error) {
		<-release
		return nil, nil
	})
	if !errors.Is(err, errStatfsTimeout) {
		t.Fatalf("err = %v, want errStatfsTimeout", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestUsageWithTimeoutSkipsPendingMount(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	fake := func(string) (*disk.UsageStat, error) {
		calls.Add(1)
		<-release
		return &disk.UsageStat{Total: 1}, nil
	}
	if _, err := usageWithTimeout("/pend", 20*time.Millisecond, fake); !errors.Is(err, errStatfsTimeout) {
		t.Fatalf("first: %v", err)
	}
	if _, err := usageWithTimeout("/pend", 20*time.Millisecond, fake); !errors.Is(err, errStatfsPending) {
		t.Fatalf("second: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		pendingMu.Lock()
		p := pendingPaths["/pend"]
		pendingMu.Unlock()
		if !p {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending entry not cleared")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := usageWithTimeout("/pend", time.Second, fake); err != nil {
		t.Fatalf("third: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func fakeParts(mounts ...string) []disk.PartitionStat {
	var parts []disk.PartitionStat
	for _, m := range mounts {
		parts = append(parts, disk.PartitionStat{Device: "/dev" + m, Mountpoint: m, Fstype: "apfs", Opts: []string{"rw"}})
	}
	return parts
}

func TestCollectDisksRecordsTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := func(path string) (*disk.UsageStat, error) {
		if path == "/slow" {
			<-release
		}
		return okStatfs(path)
	}
	// statfsTimeout is 1 s, so this test takes about 1 s.
	out, errs := collectDisks(fakeParts("/a", "/slow", "/b"), fake, 10*time.Second)
	if len(out) != 2 || out[0].Mount != "/a" || out[1].Mount != "/b" {
		t.Errorf("out = %+v", out)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "disk.Usage(/slow)") {
		t.Errorf("errs = %v", errs)
	}
}

func TestCollectDisksBudget(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fake := func(string) (*disk.UsageStat, error) {
		<-release
		return nil, nil
	}
	start := time.Now()
	out, errs := collectDisks(fakeParts("/m1", "/m2", "/m3", "/m4", "/m5"), fake, 100*time.Millisecond)
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Errorf("took %s", d)
	}
	if len(out) != 0 {
		t.Errorf("out = %+v", out)
	}
	last := errs[len(errs)-1]
	if !strings.Contains(last, "time limit 100ms reached, 4 mounts not read") {
		t.Errorf("errs = %v", errs)
	}
}
