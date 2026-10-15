package collect

import "testing"

func TestHost(t *testing.T) {
	h, errs := Host()
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if h.Name == "" {
		t.Error("hostname empty")
	}
	if h.OS == "" {
		t.Error("os empty")
	}
	if h.Arch == "" {
		t.Error("arch empty")
	}
	if h.Sampler == "" {
		t.Error("sampler version empty")
	}
}
