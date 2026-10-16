package schema

// ScanVersion is the version of the scan lines. It is separate from
// SchemaVersion, which is the version of the sample.
const ScanVersion = 1

// Values of the t key of a scan line.
const (
	ScanLineProgress  = "progress"
	ScanLineResult    = "result"
	ScanLineError     = "error"
	ScanLineCancelled = "cancelled"
)

// Groups of a recommendation.
const (
	ScanGroupSafe   = "safe"
	ScanGroupReview = "review"
)

// Categories of a tree node.
const (
	ScanCatCode  = "code"
	ScanCatAI    = "ai"
	ScanCatCache = "cache"
	ScanCatSDK   = "sdk"
	ScanCatDocs  = "docs"
	ScanCatOther = "other"
	ScanCatNone  = "none"
)

// ScanLine holds the keys that each scan line has. Decode a line into
// it first to find its type.
type ScanLine struct {
	T string `json:"t"`
	V int    `json:"v"`
}

// ScanProgress is a progress line. The sampler writes one at most once
// each --progress-interval.
type ScanProgress struct {
	T            string `json:"t"`
	V            int    `json:"v"`
	Files        int64  `json:"files"`
	Dirs         int64  `json:"dirs"`
	Bytes        int64  `json:"bytes"`
	NoAccessDirs int64  `json:"no_access_dirs"`
	Current      string `json:"current"`
	ElapsedMs    int64  `json:"elapsed_ms"`
	// UsedBytes is the used space of the filesystem that holds the
	// root. It is 0 when the sampler cannot read it.
	UsedBytes int64 `json:"used_bytes"`
	// Top is the largest folders directly below the root so far, the
	// largest first. It has 5 items at most.
	Top []ScanTop `json:"top"`
}

// ScanTop is one folder in ScanProgress.Top.
type ScanTop struct {
	Path string `json:"path"`
	B    int64  `json:"b"`
}

// ScanResult is the result line. It is the last line of a scan that
// did not fail and was not cancelled.
type ScanResult struct {
	T          string `json:"t"`
	V          int    `json:"v"`
	Root       string `json:"root"`
	OS         string `json:"os"`
	User       string `json:"user"`
	Hostname   string `json:"hostname"`
	Home       string `json:"home"`
	StartedTS  string `json:"started_ts"`
	DurationMs int64  `json:"duration_ms"`
	Files      int64  `json:"files"`
	Dirs       int64  `json:"dirs"`
	// ScannedBytes is the allocated size of all files that the walk
	// read. It is equal to Tree.B.
	ScannedBytes int64        `json:"scanned_bytes"`
	FS           *ScanFS      `json:"fs"`
	NoAccess     ScanNoAccess `json:"no_access"`
	Tree         ScanNode     `json:"tree"`
	Recs         []ScanRec    `json:"recs"`
	Errors       []string     `json:"errors"`
	// Limits are the prune limits of this result. A refresh of a
	// folder uses them to prune the same way.
	Limits *ScanLimits `json:"limits,omitempty"`
}

// ScanLimits are the prune limits of a ScanResult.
type ScanLimits struct {
	MaxDepth int `json:"max_depth"`
	// MinBytes is the effective floor: min(min-bytes, min-frac × root
	// bytes), and 1 at least.
	MinBytes int64 `json:"min_bytes"`
}

// ScanFS is the filesystem that holds the root.
type ScanFS struct {
	Mount string `json:"mount"`
	Total int64  `json:"total"`
	Used  int64  `json:"used"`
	Free  int64  `json:"free"`
}

// ScanNoAccess is the folders that the walk could not read.
type ScanNoAccess struct {
	// Dirs has 50 paths at most. Count is the full number.
	Dirs  []string `json:"dirs"`
	Count int64    `json:"count"`
	// BytesEst is fs.used minus scanned_bytes when the root is a mount
	// point. It is null in other cases.
	BytesEst *int64 `json:"bytes_est"`
}

// ScanNode is one node of the pruned tree. The size of a node is the
// sum of the sizes of its children. The root node has the root path as
// its name. Other nodes have one path segment, or more segments joined
// with the path separator when the sampler joined a chain of folders
// that each have one child.
type ScanNode struct {
	N   string `json:"n"`
	B   int64  `json:"b"`
	Cat string `json:"cat"`
	// ID is set only on nodes that a recommendation points to.
	ID string `json:"id,omitempty"`
	// F is true for a file.
	F bool `json:"f,omitempty"`
	// O is true for the node that holds the small files and folders of
	// its parent.
	O bool       `json:"o,omitempty"`
	C []ScanNode `json:"c,omitempty"`
}

// ScanRec is one cleanup recommendation.
type ScanRec struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Label string `json:"label"`
	// Path is for display. It starts with ~ for paths below the home
	// folder.
	Path     string   `json:"path"`
	Bytes    int64    `json:"bytes"`
	Elevated bool     `json:"elevated"`
	NodeIDs  []string `json:"node_ids"`
	Cmd      ScanCmd  `json:"cmd"`
}

// ScanCmd holds the commands of a recommendation. Each line is one
// command. The list for the shell of the host OS is never empty: sh on
// Linux and macOS, pwsh on Windows.
type ScanCmd struct {
	Sh   []string `json:"sh"`
	Pwsh []string `json:"pwsh"`
}

// ScanError is the line of a scan that failed.
type ScanError struct {
	T       string `json:"t"`
	V       int    `json:"v"`
	Message string `json:"message"`
}

// ScanCancelled is the line of a scan that stopped on stdin EOF,
// SIGTERM or SIGINT.
type ScanCancelled struct {
	T string `json:"t"`
	V int    `json:"v"`
}
