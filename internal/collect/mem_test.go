package collect

import "testing"

func TestMemPlausible(t *testing.T) {
	m, s, errs := Mem()
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if m.Total <= 0 {
		t.Errorf("mem.Total should be > 0, got %d", m.Total)
	}
	if m.Used < 0 || m.Used > m.Total {
		t.Errorf("mem.Used out of range: used=%d total=%d", m.Used, m.Total)
	}
	// Swap may legitimately be zero; just ensure sane bounds.
	if s.Used < 0 || s.Total < 0 {
		t.Errorf("swap negative: %+v", s)
	}
	if s.Total > 0 && s.Used > s.Total {
		t.Errorf("swap.Used > swap.Total: %+v", s)
	}
}
