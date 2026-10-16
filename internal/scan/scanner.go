package scan

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/towertail/sampler/pkg/schema"
)

// Config holds the flags of the scan command.
type Config struct {
	// Root is the folder to scan. Empty means the home folder. A
	// leading ~ is the home folder.
	Root     string
	MaxDepth int
	MinBytes int64
	// MinFrac is a fraction of the root bytes. 0 sets no fraction
	// floor. A negative value is the default.
	MinFrac          float64
	ProgressInterval time.Duration
}

// Defaults of the scan flags.
const (
	DefaultMaxDepth = 6
	DefaultMinBytes = 50_000_000
	DefaultMinFrac  = 0.002
)

// Scanner runs one scan.
type Scanner struct {
	cfg  Config
	os   string
	home string
	now  func() time.Time
}

// NewScanner returns a Scanner for cfg.
func NewScanner(cfg Config) *Scanner {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = DefaultMaxDepth
	}
	if cfg.MinBytes <= 0 {
		cfg.MinBytes = DefaultMinBytes
	}
	if cfg.MinFrac < 0 {
		cfg.MinFrac = DefaultMinFrac
	}
	home, _ := os.UserHomeDir()
	return &Scanner{cfg: cfg, os: runtime.GOOS, home: home, now: time.Now}
}

// Run scans the root. It returns ctx.Err() when ctx ends first.
func (s *Scanner) Run(ctx context.Context, progress func(schema.ScanProgress)) (schema.ScanResult, error) {
	start := s.now()
	root, err := s.root()
	if err != nil {
		return schema.ScanResult{}, err
	}
	var errs []string
	fsInfo, err := s.filesystem(root)
	if err != nil {
		errs = append(errs, err.Error())
	}

	rules := RulesFor(s.os)
	opts := Options{
		Root:             root,
		OS:               s.os,
		KeepFileBytes:    min(s.cfg.MinBytes, 1<<20),
		Globs:            rules.Globs(),
		ProgressInterval: s.cfg.ProgressInterval,
	}
	if fsInfo != nil {
		opts.UsedBytes = fsInfo.Used
	}
	// On macOS the data volume is mounted below / and firmlinked into
	// it. Enter its device, but not its mount path, so that each file
	// counts once.
	if s.os == "darwin" && root == "/" {
		const data = "/System/Volumes/Data"
		if st, err := lstat(data); err == nil {
			opts.Devices = append(opts.Devices, st.dev)
			opts.Skip = append(opts.Skip, data)
		}
	}

	t, err := NewWalker(opts).Run(ctx, progress)
	if err != nil {
		return schema.ScanResult{}, err
	}
	res := s.result(t, start, fsInfo, errs)
	if u, err := user.Current(); err == nil {
		res.User = u.Username
	}
	res.Hostname, _ = os.Hostname()
	return res, nil
}

// result prunes the tree, finds the recommendations and makes the
// result line. It does not set the user and the host name.
func (s *Scanner) result(t *Tree, start time.Time, fsInfo *schema.ScanFS, errs []string) schema.ScanResult {
	floor := t.Prune(s.cfg.MinBytes, s.cfg.MinFrac, s.cfg.MaxDepth)
	t.categorize(s.home)
	recs := RulesFor(s.os).Apply(t, s.home, start)
	res := schema.ScanResult{
		T:            schema.ScanLineResult,
		V:            schema.ScanVersion,
		Root:         t.RootPath,
		OS:           s.os,
		Home:         s.home,
		StartedTS:    schema.FormatTS(start),
		DurationMs:   s.now().Sub(start).Milliseconds(),
		Files:        t.Files,
		Dirs:         t.Dirs,
		ScannedBytes: t.Root.bytes,
		FS:           fsInfo,
		NoAccess:     schema.ScanNoAccess{Dirs: t.NoAccess, Count: t.NoAccessCount},
		Tree:         t.Schema(),
		Recs:         recs,
		Errors:       errs,
		Limits:       &schema.ScanLimits{MaxDepth: s.cfg.MaxDepth, MinBytes: floor},
	}
	if res.NoAccess.Dirs == nil {
		res.NoAccess.Dirs = []string{}
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	if fsInfo != nil && fsInfo.Mount == t.RootPath {
		est := max(0, fsInfo.Used-res.ScannedBytes)
		res.NoAccess.BytesEst = &est
	}
	return res
}

// root returns the clean, absolute root with no symlinks.
func (s *Scanner) root() (string, error) {
	p := s.cfg.Root
	switch {
	case p == "" || p == "~":
		p = s.home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		p = filepath.Join(s.home, p[2:])
	}
	if p == "" {
		return "", fmt.Errorf("no home folder")
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// filesystem returns the filesystem that holds root: the mount point
// with the longest prefix of root.
func (s *Scanner) filesystem(root string) (*schema.ScanFS, error) {
	// All partitions, so that an overlay root in a container counts.
	parts, err := disk.Partitions(true)
	if err != nil {
		return nil, fmt.Errorf("disk.Partitions: %w", err)
	}
	t := Tree{OS: s.os, sep: string(filepath.Separator)}
	mount := ""
	for _, p := range parts {
		if _, ok := t.rel(p.Mountpoint, root); ok && len(p.Mountpoint) > len(mount) {
			mount = p.Mountpoint
		}
	}
	if mount == "" {
		return nil, fmt.Errorf("no mount point for %s", root)
	}
	u, err := disk.Usage(mount)
	if err != nil {
		return nil, fmt.Errorf("disk.Usage(%s): %w", mount, err)
	}
	return &schema.ScanFS{Mount: mount, Total: int64(u.Total), Used: int64(u.Used), Free: int64(u.Free)}, nil
}

// ParseSize reads a size such as 50MB, 1.5GB or 512KiB. KB, MB, GB and
// TB are powers of 1000. KiB, MiB, GiB and TiB are powers of 1024. A
// number with no unit is bytes.
func ParseSize(v string) (int64, error) {
	units := []struct {
		suffix string
		mult   float64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
		{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"B", 1},
	}
	s := strings.TrimSpace(v)
	mult := 1.0
	for _, u := range units {
		if n, ok := strings.CutSuffix(strings.ToUpper(s), strings.ToUpper(u.suffix)); ok {
			s, mult = strings.TrimSpace(s[:len(n)]), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("bad size %q", v)
	}
	return int64(f * mult), nil
}
