package collect

import (
	"strings"
	"testing"
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
