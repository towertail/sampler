package collect

import "testing"

func TestHealthDarwinMemPressure(t *testing.T) {
	h, _ := Health()
	if h.MemPressure == nil {
		t.Fatal("MemPressure is nil")
	}
	if l := *h.MemPressure; l != 1 && l != 2 && l != 4 {
		t.Errorf("MemPressure = %d, want 1, 2 or 4", l)
	}
	if h.MemPressurePct == nil {
		t.Fatal("MemPressurePct is nil")
	}
	if p := *h.MemPressurePct; p < 0 || p > 100 {
		t.Errorf("MemPressurePct = %d, want 0…100", p)
	}
}
