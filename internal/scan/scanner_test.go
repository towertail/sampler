package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestResultLimits(t *testing.T) {
	files := []fakeFile{{"a/x", 10 * gb, fresh}, {"b/y", 5 * gb, fresh}}
	for _, tc := range []struct {
		name     string
		minBytes int64
		minFrac  float64
		want     int64
	}{
		{"fraction wins", DefaultMinBytes, DefaultMinFrac, 30 * mb},
		{"min-bytes wins", 10 * mb, DefaultMinFrac, 10 * mb},
		{"no fraction", DefaultMinBytes, 0, DefaultMinBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Scanner{cfg: Config{MaxDepth: 4, MinBytes: tc.minBytes, MinFrac: tc.minFrac}, os: "linux", home: "/home/dev", now: time.Now}
			res := s.result(fakeTree("linux", "/data", files), goldenNow, nil, nil)
			if l := res.Limits; l == nil || l.MaxDepth != 4 || l.MinBytes != tc.want {
				t.Errorf("limits = %+v, want depth 4 and %d bytes", l, tc.want)
			}
		})
	}
}

// TestSubtreeScan checks that a scan of a folder with the limits of the
// full scan gives the same nodes as that folder in the full scan.
func TestSubtreeScan(t *testing.T) {
	var files []fakeFile
	for _, g := range goldens {
		if g.os == "darwin" {
			files = g.files
		}
	}
	full := &Scanner{cfg: Config{MaxDepth: DefaultMaxDepth, MinBytes: DefaultMinBytes, MinFrac: DefaultMinFrac}, os: "darwin", home: "/Users/dev", now: time.Now}
	fres := full.result(fakeTree("darwin", "/Users/dev", files), goldenNow, nil, nil)

	// Library/Developer/Xcode is at depth 3. In the pruned tree it is
	// the chain node Developer/Xcode.
	const sub = "Library/Developer/Xcode"
	var subFiles []fakeFile
	for _, f := range files {
		if rest, ok := strings.CutPrefix(f.path, sub+"/"); ok {
			subFiles = append(subFiles, fakeFile{rest, f.bytes, f.mtime})
		}
	}
	part := &Scanner{cfg: Config{MaxDepth: fres.Limits.MaxDepth - 3, MinBytes: fres.Limits.MinBytes, MinFrac: 0}, os: "darwin", home: "/Users/dev", now: time.Now}
	pres := part.result(fakeTree("darwin", "/Users/dev/"+sub, subFiles), goldenNow, nil, nil)

	var want *schema.ScanNode
	for i, n := range fres.Tree.C {
		if n.N == "Library" {
			for j, k := range n.C {
				if k.N == "Developer/Xcode" {
					want = &fres.Tree.C[i].C[j]
				}
			}
		}
	}
	if want == nil {
		t.Fatal("no Library/Developer/Xcode in the full tree")
	}
	got := pres.Tree
	got.N = want.N
	strip := func(n schema.ScanNode) string {
		var b strings.Builder
		var walk func(n schema.ScanNode, depth int)
		walk = func(n schema.ScanNode, depth int) {
			fmt.Fprintf(&b, "%s%s %d %s\n", strings.Repeat(" ", depth), n.N, n.B, n.Cat)
			for _, c := range n.C {
				walk(c, depth+1)
			}
		}
		walk(n, 0)
		return b.String()
	}
	if g, w := strip(got), strip(*want); g != w {
		t.Errorf("subtree scan:\n%s\nwant:\n%s", g, w)
	}
	var ids []string
	for _, r := range pres.Recs {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); !strings.Contains(got, "xcode-derived-data") {
		t.Errorf("recs = %s, want xcode-derived-data", got)
	}
}

func TestRulesBelowHome(t *testing.T) {
	files := []fakeFile{{"Developer/Xcode/DerivedData/App-x/Build/o", 3 * gb, fresh}}
	s := &Scanner{cfg: Config{MaxDepth: DefaultMaxDepth, MinBytes: DefaultMinBytes, MinFrac: DefaultMinFrac}, os: "darwin", home: "/Users/dev", now: time.Now}
	res := s.result(fakeTree("darwin", "/Users/dev/Library", files), goldenNow, nil, nil)
	if len(res.Recs) != 1 || res.Recs[0].ID != "xcode-derived-data" || res.Recs[0].Path != "~/Library/Developer/Xcode/DerivedData" {
		t.Errorf("recs = %+v, want xcode-derived-data", res.Recs)
	}
}
