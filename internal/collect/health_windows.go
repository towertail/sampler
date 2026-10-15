package collect

import (
	"github.com/shirou/gopsutil/v4/process"
	"github.com/towertail/sampler/pkg/schema"
)

// Windows has no zombies, PSI, or fixed PID and handle limits, so only
// the process count is set.
func healthPlatform(h *schema.HealthInfo) []string {
	pids, err := process.Pids()
	if err != nil {
		return []string{"health.procs: " + err.Error()}
	}
	h.Procs = len(pids)
	return nil
}
