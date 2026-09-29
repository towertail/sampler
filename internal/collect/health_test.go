package collect

import (
	"testing"

	"github.com/towertail/sampler/pkg/schema"
)

func TestApplyProcEntriesCountsZombiesByParent(t *testing.T) {
	entries := []procEntry{
		{pid: 1, ppid: 0, name: "init"},
		{pid: 10, ppid: 1, name: "firebolt"},
		{pid: 20, ppid: 1, name: "cron"},
		{pid: 11, ppid: 10, name: "oom_tripwire", zombie: true},
		{pid: 12, ppid: 10, name: "oom_tripwire", zombie: true},
		{pid: 13, ppid: 10, name: "oom_tripwire", zombie: true},
		{pid: 21, ppid: 20, name: "sh", zombie: true},
	}
	var h schema.HealthInfo
	applyProcEntries(&h, entries)
	if h.Procs != 7 {
		t.Errorf("Procs = %d, want 7", h.Procs)
	}
	if h.Zombies == nil || *h.Zombies != 4 {
		t.Fatalf("Zombies = %v, want 4", h.Zombies)
	}
	if len(h.ZombieParents) != 2 {
		t.Fatalf("ZombieParents = %+v", h.ZombieParents)
	}
	if p := h.ZombieParents[0]; p.PID != 10 || p.Name != "firebolt" || p.Count != 3 {
		t.Errorf("top parent = %+v", p)
	}
}

func TestApplyProcEntriesNoZombies(t *testing.T) {
	var h schema.HealthInfo
	applyProcEntries(&h, []procEntry{{pid: 1, name: "init"}})
	if h.Zombies == nil || *h.Zombies != 0 {
		t.Errorf("Zombies = %v, want 0", h.Zombies)
	}
	if h.ZombieParents != nil {
		t.Errorf("ZombieParents should be nil, got %+v", h.ZombieParents)
	}
}

func TestApplyProcEntriesCapsParents(t *testing.T) {
	var entries []procEntry
	for i := int32(1); i <= 5; i++ {
		entries = append(entries, procEntry{pid: i, name: "p"})
		entries = append(entries, procEntry{pid: 100 + i, ppid: i, zombie: true})
	}
	var h schema.HealthInfo
	applyProcEntries(&h, entries)
	if len(h.ZombieParents) != maxZombieParents {
		t.Errorf("got %d parents, want %d", len(h.ZombieParents), maxZombieParents)
	}
}

func TestHealthLive(t *testing.T) {
	h, errs := Health()
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}
	if h.Procs < 1 {
		t.Errorf("Procs = %d", h.Procs)
	}
}
