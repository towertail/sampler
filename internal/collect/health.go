package collect

import (
	"sort"

	"github.com/towertail/sampler/pkg/schema"
)

// maxZombieParents caps how many zombie parents a sample carries.
const maxZombieParents = 3

// Health collects cheap host-level health signals. It does one light
// pass over the process table (state + parent only) and reads a few
// kernel counters. Each platform file fills what the OS exposes.
func Health() (schema.HealthInfo, []string) {
	var h schema.HealthInfo
	errs := healthPlatform(&h)
	return h, errs
}

// procEntry is the minimum per-process data the health pass needs.
type procEntry struct {
	pid    int32
	ppid   int32
	name   string
	zombie bool
}

// applyProcEntries sets the process count, zombie count, and the top
// zombie parents from one pass over the process table.
func applyProcEntries(h *schema.HealthInfo, entries []procEntry) {
	h.Procs = len(entries)
	names := make(map[int32]string, len(entries))
	byParent := map[int32]int{}
	zombies := 0
	for _, e := range entries {
		names[e.pid] = e.name
		if e.zombie {
			zombies++
			byParent[e.ppid]++
		}
	}
	h.Zombies = &zombies
	if zombies == 0 {
		return
	}
	parents := make([]schema.ZombieParent, 0, len(byParent))
	for pid, n := range byParent {
		parents = append(parents, schema.ZombieParent{PID: pid, Name: names[pid], Count: n})
	}
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].Count != parents[j].Count {
			return parents[i].Count > parents[j].Count
		}
		return parents[i].PID < parents[j].PID
	})
	if len(parents) > maxZombieParents {
		parents = parents[:maxZombieParents]
	}
	h.ZombieParents = parents
}
