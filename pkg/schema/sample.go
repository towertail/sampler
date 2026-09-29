package schema

import "time"

const SchemaVersion = 1

const tsLayout = "2006-01-02T15:04:05.000Z"

type Sample struct {
	V      int           `json:"v"`
	TS     string        `json:"ts"`
	Host   HostInfo      `json:"host"`
	CPU    CPUInfo       `json:"cpu"`
	Mem    MemInfo       `json:"mem"`
	Swap   MemInfo       `json:"swap"`
	Disks  *[]DiskSample `json:"disks,omitempty"`
	DiskIO *DiskIOInfo   `json:"disk_io,omitempty"`
	Net    *NetInfo      `json:"net,omitempty"`
	Procs  *ProcList     `json:"procs,omitempty"`
	Ports  *PortList     `json:"ports,omitempty"`
	Health *HealthInfo   `json:"health,omitempty"`
	Errors []string      `json:"errors"`
}

// HealthInfo is a set of cheap host-level health signals. Each field
// is nil when the OS does not expose it (for example zombies and PSI
// on Windows, PSI on kernels without CONFIG_PSI).
type HealthInfo struct {
	Procs         int            `json:"procs"`
	Zombies       *int           `json:"zombies,omitempty"`
	ZombieParents []ZombieParent `json:"zombie_parents,omitempty"`
	PidsUsed      *int64         `json:"pids_used,omitempty"`
	PidsMax       *int64         `json:"pids_max,omitempty"`
	FilesUsed     *int64         `json:"files_used,omitempty"`
	FilesMax      *int64         `json:"files_max,omitempty"`
	PSI           *PSIInfo       `json:"psi,omitempty"`
	MemPressure   *int           `json:"mem_pressure,omitempty"`
}

// ZombieParent is a process that holds unreaped zombie children.
type ZombieParent struct {
	PID   int32  `json:"pid"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// PSIInfo is Linux pressure stall information, avg10 in percent.
type PSIInfo struct {
	CPUSome float64 `json:"cpu_some"`
	MemSome float64 `json:"mem_some"`
	MemFull float64 `json:"mem_full"`
	IOSome  float64 `json:"io_some"`
	IOFull  float64 `json:"io_full"`
}

// ProcList is the per-process top-N slice plus the meta the Mac app needs to
// know how to display it: whether the sampler ran as root (so "missing" rows
// are because the user doesn't have visibility), how many rows were asked
// for, and the total/visible counts on the host.
type ProcList struct {
	Root    bool `json:"root"`
	TopN    int  `json:"top_n"`
	Total   int  `json:"total"`
	Visible int  `json:"visible"`
	// Skipped is true when Total exceeded the scan limit and the
	// per-process scan did not run. Items is then empty.
	Skipped bool         `json:"skipped,omitempty"`
	Items   []ProcSample `json:"items"`
}

// ProcSample is one process. Fields that require elevated access are
// omitted when the sampler can't read them — zero-valued rather than
// emitting nonsense. CPUPct is 0-100 (aggregate across cores, matches
// top(1) behavior on the host). ReadBytes/WriteBytes are lifetime
// cumulative disk I/O byte counters; nil when the sampler couldn't
// read them (Linux: /proc/<pid>/io denied without CAP_SYS_PTRACE or
// process ownership; macOS: gopsutil does not implement per-proc I/O).
type ProcSample struct {
	PID        int32   `json:"pid"`
	PPID       int32   `json:"ppid,omitempty"`
	Name       string  `json:"name"`
	Cmd        string  `json:"cmd,omitempty"`
	User       string  `json:"user,omitempty"`
	CPUPct     float64 `json:"cpu_pct"`
	RSS        int64   `json:"rss"`
	Threads    int32   `json:"threads,omitempty"`
	State      string  `json:"state,omitempty"`
	StartTS    string  `json:"start_ts,omitempty"`
	ReadBytes  *int64  `json:"read_bytes,omitempty"`
	WriteBytes *int64  `json:"write_bytes,omitempty"`
}

type HostInfo struct {
	Name      string `json:"name"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Kernel    string `json:"kernel"`
	UptimeS   int64  `json:"uptime_s"`
	Sampler   string `json:"sampler"`
	MachineID string `json:"machine_id,omitempty"`
}

type CPUInfo struct {
	Pct      float64 `json:"pct"`
	Load1    float64 `json:"load_1"`
	Load5    float64 `json:"load_5"`
	Load15   float64 `json:"load_15"`
	Cores    int     `json:"cores"`
	UserMs   int64   `json:"user_ms,omitempty"`
	SystemMs int64   `json:"system_ms,omitempty"`
	IdleMs   int64   `json:"idle_ms,omitempty"`
	IowaitMs int64   `json:"iowait_ms,omitempty"`
	IrqMs    int64   `json:"irq_ms,omitempty"`
	NiceMs   int64   `json:"nice_ms,omitempty"`
	StealMs  int64   `json:"steal_ms,omitempty"`
	TotalMs  int64   `json:"total_ms,omitempty"`
}

type MemInfo struct {
	Used  int64 `json:"used"`
	Total int64 `json:"total"`
}

type DiskSample struct {
	Mount string `json:"mount"`
	Fs    string `json:"fs"`
	Used  int64  `json:"used"`
	Total int64  `json:"total"`
	// Inode counts. Omitted when the filesystem reports none (btrfs, NTFS).
	InodesUsed  int64 `json:"inodes_used,omitempty"`
	InodesTotal int64 `json:"inodes_total,omitempty"`
}

type NetInfo struct {
	RxBps int64 `json:"rx_bps"`
	TxBps int64 `json:"tx_bps"`
	RxCum int64 `json:"rx_cum"`
	TxCum int64 `json:"tx_cum"`
}

// DiskIOInfo is system-wide disk I/O. Top-level fields are the aggregate
// sum across physical devices (partitions rolled up into their parent);
// `Devices` carries the same numbers broken out per physical device so
// the Mac app can chart a specific disk. The Mac app recomputes rates
// from cumulative counter deltas across polls (same pattern as NetInfo).
// ReadBps/WriteBps are populated from a short in-sampler delta window
// so one-shot mode produces usable numbers without prior state; zero in
// streaming mode until the second tick.
type DiskIOInfo struct {
	ReadBps  int64              `json:"read_bps"`
	WriteBps int64              `json:"write_bps"`
	ReadCum  int64              `json:"read_cum"`
	WriteCum int64              `json:"write_cum"`
	Devices  []DiskIODeviceInfo `json:"devices,omitempty"`
}

// DiskIODeviceInfo is per-block-device I/O counters. Name is the kernel
// device name (e.g. "nvme0n1", "sda") — partitions are excluded to
// avoid double-counting their parent device.
type DiskIODeviceInfo struct {
	Name     string `json:"name"`
	ReadBps  int64  `json:"read_bps"`
	WriteBps int64  `json:"write_bps"`
	ReadCum  int64  `json:"read_cum"`
	WriteCum int64  `json:"write_cum"`
}

// PortList is a per-PID aggregate view of open sockets on the host.
// Refreshed on a slower cadence than the rest of the sample (default 10s)
// because enumerating connections walks /proc/<pid>/fd/* on Linux which
// is the most expensive collector by an order of magnitude. Between
// refreshes the cached snapshot is re-emitted unchanged.
//
// CollectedTS is the wall-clock when the snapshot was actually built —
// distinct from the sample's top-level TS so the client can render
// "ports as of HH:MM:SS" instead of pretending the data is fresh.
//
// MaxConn is the cap passed to gopsutil's ConnectionsMax. Truncated is
// true when the cap was hit (some connections are not represented).
// Total is the number of connections observed (≤ MaxConn).
type PortList struct {
	Root        bool       `json:"root"`
	CollectedTS string     `json:"collected_ts"`
	MaxConn     int        `json:"max_conn"`
	Truncated   bool       `json:"truncated"`
	Total       int        `json:"total"`
	Items       []PortItem `json:"items"`
}

// PortItem is one process's port footprint. ListenTCP/ListenUDP are
// sorted, deduped local listening port numbers. EstOut is the count of
// outbound ESTABLISHED TCP connections; EstIn is the count of inbound
// ESTABLISHED TCP connections (peer connecting to one of our listening
// ports). UDPSockets counts UDP sockets without a peer (UDP has no
// connection state — this is "open udp ports"). TopRemotePorts are the
// most-frequently-seen remote ports for outbound connections (truncated
// to 5 entries) so the UI can spot "talking to a lot of :443" patterns.
type PortItem struct {
	PID            int32       `json:"pid"`
	Name           string      `json:"name,omitempty"`
	User           string      `json:"user,omitempty"`
	ListenTCP      []uint32    `json:"listen_tcp,omitempty"`
	ListenUDP      []uint32    `json:"listen_udp,omitempty"`
	EstOut         int         `json:"est_out"`
	EstIn          int         `json:"est_in"`
	UDPSockets     int         `json:"udp_sockets,omitempty"`
	TopRemotePorts []PortCount `json:"top_remote_ports,omitempty"`
}

// PortCount is a remote port + how many established outbound connections
// from this process target it.
type PortCount struct {
	Port  uint32 `json:"port"`
	Count int    `json:"count"`
}

func FormatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
}
