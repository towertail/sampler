package scan

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/towertail/sampler/pkg/schema"
)

func TestScannerRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sets HOME")
	}
	home := tempRoot(t)
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".npm", "_cacache", "big"), 12<<20)
	writeFile(t, filepath.Join(home, "sub", "small"), 100)

	if p, err := NewScanner(Config{Root: "~/sub"}).root(); err != nil || p != filepath.Join(home, "sub") {
		t.Errorf("root of ~/sub = %s, %v", p, err)
	}
	s := NewScanner(Config{MinBytes: 1 << 20})
	res, err := s.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.T != schema.ScanLineResult || res.V != schema.ScanVersion {
		t.Errorf("t/v = %s/%d", res.T, res.V)
	}
	if res.Root != home || res.Tree.N != home {
		t.Errorf("root = %s, tree = %s, want %s", res.Root, res.Tree.N, home)
	}
	if res.Home != home || res.OS != runtime.GOOS || res.User == "" || res.Hostname == "" {
		t.Errorf("host fields = %q %q %q %q", res.Home, res.OS, res.User, res.Hostname)
	}
	if res.Files != 2 || res.ScannedBytes != res.Tree.B || res.FS == nil || res.FS.Total == 0 {
		t.Errorf("files = %d, bytes = %d/%d, fs = %+v", res.Files, res.ScannedBytes, res.Tree.B, res.FS)
	}
	if res.NoAccess.Dirs == nil || res.Errors == nil || res.Recs == nil {
		t.Error("nil list in the result")
	}
	if res.NoAccess.BytesEst != nil {
		t.Error("bytes_est is set for a root that is not a mount point")
	}
	if len(res.Recs) != 1 || res.Recs[0].ID != "npm-cache" || res.Recs[0].Path != "~/.npm/_cacache" {
		t.Errorf("recs = %+v, want npm-cache", res.Recs)
	}
}

func TestScannerBadRoot(t *testing.T) {
	s := NewScanner(Config{Root: filepath.Join(tempRoot(t), "missing")})
	if _, err := s.Run(context.Background(), nil); err == nil {
		t.Error("got no error for a missing root")
	}
}
