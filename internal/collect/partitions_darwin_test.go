//go:build darwin

package collect

import "testing"

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
		if !hasOpt(p.Opts, "rw") && !hasOpt(p.Opts, "ro") {
			t.Errorf("opts has no rw or ro: %v", p.Opts)
		}
		return
	}
	t.Fatal("/ not in partitions")
}
