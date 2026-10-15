package collect

import (
	"testing"
	"time"
)

func TestCPUPlausibleRanges(t *testing.T) {
	c, errs := CPU(50 * time.Millisecond)
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if c.Cores <= 0 {
		t.Errorf("cores should be > 0, got %d", c.Cores)
	}
	if c.Pct < 0 || c.Pct > 100*float64(c.Cores+1) {
		t.Errorf("pct out of range: %v (cores=%d)", c.Pct, c.Cores)
	}
	// Load averages can't be negative.
	if c.Load1 < 0 || c.Load5 < 0 || c.Load15 < 0 {
		t.Errorf("negative load avg: %+v", c)
	}
}
