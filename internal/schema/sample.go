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
	Errors []string      `json:"errors"`
}

// ProcList is the per-process top-N slice plus the meta the Mac app needs to
// know how to display it: whether the sampler ran as root (so "missing" rows
// are because the user doesn't have visibility), how many rows were asked
// for, and the total/visible counts on the host.
type ProcList struct {
	Root    bool         `json:"root"`
	TopN    int          `json:"top_n"`
	Total   int          `json:"total"`
	Visible int          `json:"visible"`
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
	Sampler     string `json:"sampler"`
	MachineID string `json:"machine_id,omitempty"`
}

type CPUInfo struct {
	Pct       float64 `json:"pct"`
	Load1     float64 `json:"load_1"`
	Load5     float64 `json:"load_5"`
	Load15    float64 `json:"load_15"`
	Cores     int     `json:"cores"`
	UserMs    int64   `json:"user_ms,omitempty"`
	SystemMs  int64   `json:"system_ms,omitempty"`
	IdleMs    int64   `json:"idle_ms,omitempty"`
	IowaitMs  int64   `json:"iowait_ms,omitempty"`
	IrqMs     int64   `json:"irq_ms,omitempty"`
	NiceMs    int64   `json:"nice_ms,omitempty"`
	StealMs   int64   `json:"steal_ms,omitempty"`
	TotalMs   int64   `json:"total_ms,omitempty"`
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
}

type NetInfo struct {
	RxBps int64 `json:"rx_bps"`
	TxBps int64 `json:"tx_bps"`
	RxCum int64 `json:"rx_cum"`
	TxCum int64 `json:"tx_cum"`
}

// DiskIOInfo is system-wide aggregate disk I/O, summed across physical
// devices. The Mac app recomputes rates from cumulative counter deltas
// across polls (same pattern as NetInfo). ReadBps/WriteBps are populated
// from a short in-sampler delta window so one-shot mode produces usable
// numbers without prior state; zero in streaming mode until the second
// tick.
type DiskIOInfo struct {
	ReadBps   int64 `json:"read_bps"`
	WriteBps  int64 `json:"write_bps"`
	ReadCum   int64 `json:"read_cum"`
	WriteCum  int64 `json:"write_cum"`
}

func FormatTS(t time.Time) string {
	return t.UTC().Format(tsLayout)
}
