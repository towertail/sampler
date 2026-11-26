//go:build darwin

package collect

import (
	"slices"
	"testing"
)

func TestPartitionsDarwin(t *testing.T) {
	parts, err := partitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if p.Mountpoint != "/" {
			continue
		}
		if p.Fstype == "" {
			t.Errorf("empty fstype for /: %+v", p)
		}
		if !slices.Contains(p.Opts, "rw") && !slices.Contains(p.Opts, "ro") {
			t.Errorf("opts has no rw or ro: %v", p.Opts)
		}
		return
	}
	t.Fatal("/ not in partitions")
}
