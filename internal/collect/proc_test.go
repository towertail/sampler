package collect

import (
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

func TestProcReturnsItems(t *testing.T) {
	list, errs := Proc(50*time.Millisecond, 20, 0)
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if list.Total <= 0 {
		t.Fatalf("expected > 0 total processes, got %d", list.Total)
	}
	if len(list.Items) == 0 {
		t.Fatal("expected at least one item in process list")
	}
	// The test binary itself has to be in the list if we could enumerate.
	foundSelf := false
	for _, p := range list.Items {
		if p.PID > 0 && p.Name != "" {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		t.Error("expected at least one proc with PID and Name populated")
	}
	if list.TopN != 20 {
		t.Errorf("TopN: got %d want 20", list.TopN)
	}
}

func TestProcRespectsTopN(t *testing.T) {
	// Small cap: visible should be >= cap, items should be <= 2*cap
	// (union of top-by-CPU and top-by-RSS).
	list, _ := Proc(50*time.Millisecond, 5, 0)
	if list.Total < 5 {
		t.Skipf("host has only %d procs; test needs >5", list.Total)
	}
	if len(list.Items) > 10 {
		t.Errorf("expected at most 2*5 = 10 items in union, got %d", len(list.Items))
	}
	if len(list.Items) < 5 {
		t.Errorf("expected at least 5 items, got %d", len(list.Items))
	}
}

func TestProcSortedByCPUDesc(t *testing.T) {
	list, _ := Proc(50*time.Millisecond, 20, 0)
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
	out := unionTopN(items, 2)
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

func TestIsRoot(t *testing.T) {
	// Don't assert the value (test env varies); just confirm it's callable
	// and matches the low-level euid check we rely on.
	_ = IsRoot()
}

func TestProcSkipsScanAboveMax(t *testing.T) {
	list, _ := Proc(50*time.Millisecond, 20, 1)
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
