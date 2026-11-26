package schema

import "encoding/json"

// Values of the kind key of a ControlNextResponse.
const (
	ControlKindKillProcess = "kill_process"
	ControlKindDiskScan    = "disk_scan"
)

// Values of the reason key of an IngestGone.
const (
	GoneHostRemoved    = "host_removed"
	GoneAccountDeleted = "account_deleted"
	GoneCloudOff       = "cloud_off"
)

// JoinRequest is the body of POST /v1/sampler/join. The reply is an
// EnrollResponse.
type JoinRequest struct {
	JoinToken   string `json:"join_token" validate:"required"`
	DisplayName string `json:"display_name" validate:"required"`
	MachineID   string `json:"machine_id,omitempty"`
}

// EnrollResponse is the reply of the join and enroll calls. A not empty
// Endpoint replaces the endpoint of the call.
type EnrollResponse struct {
	NodeID       string `json:"node_id"`
	SamplerToken string `json:"sampler_token"`
	Endpoint     string `json:"endpoint"`
}

// IngestResponse is the 202 body of POST /v1/ingest/samples. Interval
// is a Go duration ("5s"). Update is the version that the sampler
// should run. Both are left out when the server has no order.
type IngestResponse struct {
	Accepted int    `json:"accepted"`
	Rejected int    `json:"rejected"`
	Error    string `json:"error,omitempty"`
	Interval string `json:"interval,omitempty"`
	Update   string `json:"update,omitempty"`
	// ProcCwd tells the sampler to send process folders. It is nil when the key is missing.
	ProcCwd *bool `json:"proc_cwd,omitempty"`
}

// IngestPlanFull is the 402 body of POST /v1/ingest/samples. The sampler
// pauses for RetryAfter seconds.
type IngestPlanFull struct {
	RetryAfter int `json:"retry_after"`
}

// IngestGone is the 410 body of POST /v1/ingest/samples. The sampler
// exits.
type IngestGone struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// ControlNextResponse is one message of GET /v1/control/next. When no
// message is available, the server sends HTTP 204 with no body.
type ControlNextResponse struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// KillProcessPayload is the payload of a kill_process message.
type KillProcessPayload struct {
	PID int32 `json:"pid"`
}

// DiskScanPayload is the payload of a disk_scan message. The sampler
// uses its defaults for keys that are not there.
type DiskScanPayload struct {
	JobID    string `json:"job_id"`
	Root     string `json:"root"`
	MaxDepth int    `json:"max_depth,omitempty"`
	MinBytes int64  `json:"min_bytes,omitempty"`
	// MinFrac is nil for the scan default. A refresh sends 0.
	MinFrac *float64 `json:"min_frac,omitempty"`
}

// DiskScanProgressResponse is the reply to a progress post. Cancel
// tells the sampler to stop the scan.
type DiskScanProgressResponse struct {
	Cancel bool `json:"cancel"`
}
