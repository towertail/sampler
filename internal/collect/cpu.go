package collect

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/towertail/sampler/pkg/schema"
)

// SampleWindow is the self-sampling interval used by CPU%, net throughput,
// and per-process CPU% calculations. 500ms is a compromise: short enough
// that --once completes in well under a second, long enough that per-process
// CPU% readings aren't dominated by measurement noise (processes that only
// run for a handful of ticks within the window).
const SampleWindow = 500 * time.Millisecond

func CPU(window time.Duration) (schema.CPUInfo, []string) {
	var errs []string
	c := schema.CPUInfo{}

	// Cumulative CPU time counters (aggregate across all cores).
	times, err := cpu.Times(false)
	if err != nil {
		errs = append(errs, "cpu.Times: "+err.Error())
	} else if len(times) > 0 {
		t := times[0]
		// gopsutil reports seconds as float64; convert to ms.
		c.UserMs = secondsToMs(t.User)
		c.SystemMs = secondsToMs(t.System)
		c.IdleMs = secondsToMs(t.Idle)
		c.IowaitMs = secondsToMs(t.Iowait)
		c.IrqMs = secondsToMs(t.Irq) + secondsToMs(t.Softirq)
		c.NiceMs = secondsToMs(t.Nice)
		c.StealMs = secondsToMs(t.Steal)
		c.TotalMs = c.UserMs + c.SystemMs + c.IdleMs + c.IowaitMs + c.IrqMs + c.NiceMs + c.StealMs
	}

	// Short-window percent: retained so single --once invocations produce a
	// usable number without requiring a prior sample. The counter-based
	// fields above are authoritative when a previous tick is available.
	pcts, err := cpu.Percent(window, false)
	if err != nil {
		errs = append(errs, "cpu.Percent: "+err.Error())
	} else if len(pcts) > 0 {
		c.Pct = pcts[0]
	}

	cores, err := cpu.Counts(true)
	if err != nil {
		errs = append(errs, "cpu.Counts: "+err.Error())
	} else {
		c.Cores = cores
	}

	avg, err := load.Avg()
	if err != nil {
		errs = append(errs, "load.Avg: "+err.Error())
	} else if avg != nil {
		c.Load1 = avg.Load1
		c.Load5 = avg.Load5
		c.Load15 = avg.Load15
	}

	return c, errs
}

func secondsToMs(s float64) int64 {
	return int64(s * 1000.0)
}
