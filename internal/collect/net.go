package collect

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/towertail/sampler/internal/schema"
)

func isIncludedIface(name string) bool {
	n := strings.ToLower(name)
	if n == "" {
		return false
	}
	prefixes := []string{
		"lo",     // loopback
		"docker", // docker bridge
		"veth",   // container veth pair
		"br-",    // docker user-bridges
		"utun",   // macOS tunnels
		"llw",    // macOS low-latency Wi-Fi awdl peer
		"awdl",   // Apple Wireless Direct Link
		"bridge", // macOS bridge
		"gif",    // generic tunnel
		"stf",    // 6to4 tunnel
		"anpi",   // macOS internal
		"ap",     // macOS access point virtual
		"vmenet", // macOS VM networking
	}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	return true
}

func sumCounters(stats []net.IOCountersStat) (rx, tx uint64) {
	for _, s := range stats {
		if !isIncludedIface(s.Name) {
			continue
		}
		rx += s.BytesRecv
		tx += s.BytesSent
	}
	return
}

// Net returns the aggregate cumulative rx/tx counters across
// non-loopback, non-virtual interfaces. The consumer is expected to
// compute rates from counter deltas between ticks; rx_bps/tx_bps are
// left at 0 because in-process self-sampling over a short window
// severely undersamples bursty traffic.
func Net(window time.Duration) (schema.NetInfo, []string) {
	_ = window
	var errs []string
	var n schema.NetInfo

	per, err := net.IOCounters(true)
	if err != nil {
		errs = append(errs, "net.IOCounters(per): "+err.Error())
		return n, errs
	}
	rx, tx := sumCounters(per)
	n.RxCum = int64(rx)
	n.TxCum = int64(tx)
	n.RxBps = 0
	n.TxBps = 0
	return n, errs
}
