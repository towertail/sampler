package collect

import (
	"slices"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/towertail/sampler/pkg/schema"
)

// SOCK_STREAM (TCP) / SOCK_DGRAM (UDP) — same on every supported OS.
const (
	sockStream = 1
	sockDgram  = 2
)

// DefaultPortsMaxConn is the default cap on connections enumerated per
// refresh. A "regular" server has a couple hundred; busy ones might hit
// 800–1500. 2000 is the "something is genuinely wrong, don't melt the
// sampler" ceiling. ConnectionsMax stops walking once the cap is hit.
const DefaultPortsMaxConn = 2000

// DefaultPortsInterval is how often the streaming sampler rebuilds the
// per-process ports table. Re-enumerating connections is the most
// expensive collector by an order of magnitude on Linux because it
// walks /proc/<pid>/fd/* — keep this slower than the main 1s tick.
const DefaultPortsInterval = 10 * time.Second

// Ports collects open sockets and aggregates them per-PID. maxConn caps
// the underlying gopsutil call so a host with thousands of connections
// can't stall the sampler. When the cap is hit, the returned PortList
// has Truncated=true.
//
// Process names/users are looked up with gopsutil/process for any PID
// that owns at least one socket — name lookup hits a cached
// /proc/<pid>/comm path, which is fast (<1µs/PID).
//
// Listening sockets and outbound connections are reported separately
// per process; processes with no sockets are dropped from the output.
func Ports(maxConn int) (schema.PortList, []string) {
	if maxConn <= 0 {
		maxConn = DefaultPortsMaxConn
	}
	var errs []string
	list := schema.PortList{
		Root:        IsRoot(),
		CollectedTS: schema.FormatTS(time.Now()),
		MaxConn:     maxConn,
		Items:       []schema.PortItem{},
	}

	// "inet" = TCP+UDP, IPv4+IPv6. One call covers everything we want.
	// Linux and Windows implement ConnectionsMax (which short-circuits
	// once the cap is hit); Darwin/BSD return "not implemented" for it,
	// so fall back to plain Connections() + manual slice. The Darwin
	// path uses lsof under the hood — measure cost in practice; if it
	// becomes a problem the workaround is per-PID enumeration.
	conns, err := net.ConnectionsMax("inet", maxConn)
	if err != nil || len(conns) == 0 {
		// Either unimplemented (Darwin/BSD) or a real error. Try the
		// plain call and treat its error as authoritative — if both
		// fail we surface the second one.
		var err2 error
		conns, err2 = net.Connections("inet")
		if err2 != nil {
			errs = append(errs, "net.Connections: "+err2.Error())
			return list, errs
		}
	}
	list.Total = len(conns)
	if len(conns) >= maxConn {
		list.Truncated = true
		conns = conns[:maxConn]
	}

	// Aggregate by PID. PID 0 means the kernel couldn't tell us who owns
	// the socket (common for non-root on Linux when looking at other
	// users' sockets) — drop those rows so the UI isn't littered with
	// pid=0 entries we can't even name.
	type bucket struct {
		listenTCP  map[uint32]struct{}
		listenUDP  map[uint32]struct{}
		estOut     int
		estIn      int
		udpSockets int
		remotes    map[uint32]int
	}
	buckets := make(map[int32]*bucket, 64)
	listenPorts := make(map[uint32]struct{}, 16) // host's listening TCP ports — used to classify ESTABLISHED direction

	// First pass: collect listening ports across the whole host so we
	// can classify ESTABLISHED connections as in vs out.
	for _, c := range conns {
		if c.Status == "LISTEN" {
			listenPorts[c.Laddr.Port] = struct{}{}
		}
	}

	for _, c := range conns {
		if c.Pid <= 0 {
			continue
		}
		b, ok := buckets[c.Pid]
		if !ok {
			b = &bucket{
				listenTCP: map[uint32]struct{}{},
				listenUDP: map[uint32]struct{}{},
				remotes:   map[uint32]int{},
			}
			buckets[c.Pid] = b
		}
		switch c.Type {
		case sockStream:
			switch c.Status {
			case "LISTEN":
				b.listenTCP[c.Laddr.Port] = struct{}{}
			case "ESTABLISHED":
				// Inbound: the local port is one we listen on.
				// Outbound: we initiated to a remote port.
				if _, isListener := listenPorts[c.Laddr.Port]; isListener {
					b.estIn++
				} else {
					b.estOut++
					if c.Raddr.Port != 0 {
						b.remotes[c.Raddr.Port]++
					}
				}
			}
		case sockDgram:
			// UDP: connectionless, so "listening" vs "connected" is
			// fuzzy. If Raddr is zeroed, it's a passive socket — count
			// it as a listener if the local port is non-ephemeral
			// (heuristic: <49152), otherwise as an open udp socket.
			if c.Raddr.IP == "" || c.Raddr.Port == 0 {
				if c.Laddr.Port != 0 && c.Laddr.Port < 49152 {
					b.listenUDP[c.Laddr.Port] = struct{}{}
				} else {
					b.udpSockets++
				}
			} else {
				// UDP with a remote (e.g. an active QUIC flow). Treat
				// like outbound for accounting; UDP has no real direction.
				b.estOut++
				if c.Raddr.Port != 0 {
					b.remotes[c.Raddr.Port]++
				}
			}
		}
	}

	// Materialize PortItems with name/user lookups. Processes that died
	// between connection enumeration and now are still emitted — we keep
	// the PID, just leave name/user empty.
	items := make([]schema.PortItem, 0, len(buckets))
	for pid, b := range buckets {
		// Skip processes with literally nothing to report after filtering.
		if len(b.listenTCP) == 0 && len(b.listenUDP) == 0 && b.estOut == 0 && b.estIn == 0 && b.udpSockets == 0 {
			continue
		}
		item := schema.PortItem{
			PID:        pid,
			EstOut:     b.estOut,
			EstIn:      b.estIn,
			UDPSockets: b.udpSockets,
		}
		if p, err := process.NewProcess(pid); err == nil {
			if name, err := p.Name(); err == nil {
				item.Name = name
			}
			if user, err := p.Username(); err == nil {
				item.User = user
			}
		}
		if len(b.listenTCP) > 0 {
			item.ListenTCP = sortedKeys(b.listenTCP)
		}
		if len(b.listenUDP) > 0 {
			item.ListenUDP = sortedKeys(b.listenUDP)
		}
		if len(b.remotes) > 0 {
			item.TopRemotePorts = topRemotePorts(b.remotes, 5)
		}
		items = append(items, item)
	}

	// Stable presentation order: most outbound connections first, then
	// most inbound, then PID. Lets the UI default to "who's making lots
	// of outbound connections" without a sort step.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].EstOut != items[j].EstOut {
			return items[i].EstOut > items[j].EstOut
		}
		if items[i].EstIn != items[j].EstIn {
			return items[i].EstIn > items[j].EstIn
		}
		return items[i].PID < items[j].PID
	})
	list.Items = items
	return list, errs
}

func sortedKeys(m map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func topRemotePorts(m map[uint32]int, n int) []schema.PortCount {
	out := make([]schema.PortCount, 0, len(m))
	for port, count := range m {
		out = append(out, schema.PortCount{Port: port, Count: count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Port < out[j].Port
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
