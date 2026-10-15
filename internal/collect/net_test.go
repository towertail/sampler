package collect

import (
	"testing"
	"time"
)

func TestNetNonNegative(t *testing.T) {
	n, errs := Net(50 * time.Millisecond)
	if len(errs) > 0 {
		t.Logf("non-fatal errors: %v", errs)
	}
	if n.RxCum < 0 || n.TxCum < 0 {
		t.Errorf("cumulative counters should be non-negative: %+v", n)
	}
	if n.RxBps < 0 || n.TxBps < 0 {
		t.Errorf("bps should be non-negative: %+v", n)
	}
}

func TestIfaceFilter(t *testing.T) {
	cases := map[string]bool{
		"en0":       true,
		"eth0":      true,
		"wlan0":     true,
		"lo":        false,
		"lo0":       false,
		"docker0":   false,
		"veth1234":  false,
		"br-abc":    false,
		"utun0":     false,
		"awdl0":     false,
		"llw0":      false,
		"bridge100": false,
	}
	for name, want := range cases {
		if got := isIncludedIface(name); got != want {
			t.Errorf("isIncludedIface(%q) = %v, want %v", name, got, want)
		}
	}
}
