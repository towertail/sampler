package collect

import (
	"strings"
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
