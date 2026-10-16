package scan

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

var update = flag.Bool("update", false, "write the golden scan fixtures in testdata")

const (
	mb = 1_000_000
	gb = 1_000_000_000
)

var (
	goldenNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fresh     = goldenNow.Add(-24 * time.Hour)
	stale     = goldenNow.Add(-200 * 24 * time.Hour)
)

type golden struct {
	os, root, home string
	fs             *schema.ScanFS
	noAccess       []string
	files          []fakeFile
	dirs           []string
}

var goldens = []golden{
	{
		os: "linux", root: "/", home: "/home/dev",
		fs:       &schema.ScanFS{Mount: "/", Total: 100 * gb, Used: 31 * gb, Free: 69 * gb},
		noAccess: []string{"/root"},
		files: []fakeFile{
			{"var/cache/apt/archives/linux-image-6.8.0-45_amd64.deb", 80 * mb, fresh},
			{"var/cache/apt/archives/libllvm17_amd64.deb", 40 * mb, fresh},
			{"var/cache/apt/archives/lock", 0, fresh},
			{"var/log/journal/7f3a/system@0006168a-a1.journal", 160 * mb, stale},
			{"var/log/journal/7f3a/user-1000@0006168a-b2.journal", 40 * mb, stale},
			{"var/log/journal/7f3a/system.journal", 64 * mb, fresh},
			{"var/log/syslog.2.gz", 15 * mb, stale},
			{"var/log/nginx/access.log.3.gz", 12 * mb, stale},
			{"var/log/nginx/access.log", 30 * mb, fresh},
			{"var/crash/_usr_bin_app.1000.crash", 30 * mb, stale},
			{"var/lib/docker/overlay2/l1/diff/blob", 3 * gb, fresh},
			{"var/lib/postgresql/16/main/base/1/16384", 900 * mb, fresh},
			{"usr/lib/x86_64-linux-gnu/libLLVM.so.17", 2 * gb, stale},
			{"usr/share/doc/README", 1 * mb, stale},
			{"home/dev/.cache/pip/http-v2/a/b", 300 * mb, fresh},
			{"home/dev/.cache/go-build/00/a-d", 1200 * mb, fresh},
			{"home/dev/.npm/_cacache/content-v2/sha512/x", 500 * mb, fresh},
			{"home/dev/.ollama/models/blobs/sha256-6a0746a1", 4 * gb, fresh},
			{"home/dev/app/package.json", 1000, stale},
			{"home/dev/app/src/index.js", 20000, stale},
			{"home/dev/app/node_modules/esbuild/bin/esbuild", 400 * mb, stale},
			{"home/dev/api/go.mod", 500, fresh},
			{"home/dev/api/main.go", 30 * mb, fresh},
			{`home/dev/My Project's "data"/dump.bin`, 100 * mb, fresh},
			{"srv/shop/releases/20260801-0900/app.tar", 100 * mb, stale},
			{"srv/shop/releases/20260815-0900/app.tar", 100 * mb, stale},
			{"srv/shop/releases/20260901-0900/app.tar", 100 * mb, stale},
			{"srv/shop/releases/20260920-0900/app.tar", 100 * mb, fresh},
			{"srv/shop/releases/20260930-0900/app.tar", 100 * mb, fresh},
		},
		dirs: []string{"tmp", "home/dev/.local/share/Trash"},
	},
	{
		os: "darwin", root: "/Users/dev", home: "/Users/dev",
		fs:       &schema.ScanFS{Mount: "/System/Volumes/Data", Total: 994 * gb, Used: 610 * gb, Free: 384 * gb},
		noAccess: []string{"/Users/dev/Library/Mail"},
		files: []fakeFile{
			{"Library/Caches/go-build/0a/x-d", 2 * gb, fresh},
			{"Library/Caches/Homebrew/downloads/node.bottle.tar.gz", 60 * mb, fresh},
			{"Library/Developer/Xcode/DerivedData/Towertail-abc/Build/x.o", 3 * gb, fresh},
			{"Library/Developer/Xcode/iOS DeviceSupport/iPhone15,2 17.0 (21A329)/Symbols/dyld", 4 * gb, stale},
			{"Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw", 20 * gb, fresh},
			{"Library/Application Support/Slack/Cache/data_1", 700 * mb, fresh},
			{".ollama/models/blobs/sha256-9f43", 8 * gb, fresh},
			{".npm/_cacache/index-v5/a", 300 * mb, fresh},
			{".Trash/old.zip", 1 * gb, stale},
			{"Downloads/Docker.dmg", 600 * mb, stale},
			{"Downloads/Xcode_16.pkg", 300 * mb, stale},
			{"Downloads/report.pdf", 5 * mb, stale},
			{"Projects/web/package.json", 1000, stale},
			{"Projects/web/node_modules/next/dist/x.js", 600 * mb, stale},
			{"Projects/web/.git/objects/pack/p.pack", 90 * mb, stale},
			{"Projects/api/go.mod", 500, fresh},
			{"Projects/api/internal/db/big_test_data.json", 200 * mb, fresh},
			{"Projects/tools/package.json", 1000, fresh},
			{"Projects/tools/node_modules/x/y.js", 150 * mb, fresh},
			{"Movies/clip.mov", 5 * gb, stale},
			{"Documents/Taxes/2025.pdf", 2 * mb, stale},
		},
	},
	{
		os: "windows", root: `C:\Users\dev`, home: `C:\Users\dev`,
		fs: &schema.ScanFS{Mount: `C:\`, Total: 512 * gb, Used: 301 * gb, Free: 211 * gb},
		files: []fakeFile{
			{"AppData/Local/Temp/setup-log.txt", 900 * mb, stale},
			{"AppData/Local/Temp/chrome_drag/a.tmp", 600 * mb, stale},
			{"AppData/Local/npm-cache/_cacache/content-v2/x", 700 * mb, fresh},
			{"AppData/Local/CrashDumps/Code.exe.4412.dmp", 80 * mb, stale},
			{"AppData/Local/Docker/wsl/disk/docker_data.vhdx", 15 * gb, fresh},
			{".ollama/models/blobs/sha256-6a07", 4 * gb, fresh},
			{"Downloads/VSCodeUserSetup-x64.exe", 120 * mb, stale},
			{"Downloads/node-v22-x64.msi", 300 * mb, stale},
			{"Downloads/notes.txt", 1000, stale},
			{"Projects/$app`[v2]/package.json", 1000, stale},
			{"Projects/$app`[v2]/node_modules/electron/dist/electron.exe", 300 * mb, stale},
			{"Videos/Captures/game.mp4", 3 * gb, fresh},
		},
	},
}

// scanLines returns the fixture of one golden: two progress lines and
// the result line.
func (g golden) scanLines() ([]byte, error) {
	t := fakeTree(g.os, g.root, g.files, g.dirs...)
	t.NoAccess = g.noAccess
	t.NoAccessCount = int64(len(g.noAccess))
	s := &Scanner{
		cfg:  Config{MaxDepth: DefaultMaxDepth, MinBytes: DefaultMinBytes, MinFrac: DefaultMinFrac},
		os:   g.os,
		home: g.home,
		now:  func() time.Time { return goldenNow.Add(12340 * time.Millisecond) },
	}
	res := s.result(t, goldenNow, g.fs, []string{})
	res.User = "dev"
	res.Hostname = "dev-" + g.os

	var top []schema.ScanTop
	for _, k := range t.pruned.kids {
		if !k.other && len(top) < 5 {
			top = append(top, schema.ScanTop{Path: k.paths[0], B: k.bytes / 2})
		}
	}
	lines := []any{
		schema.ScanProgress{
			T: schema.ScanLineProgress, V: schema.ScanVersion,
			Files: res.Files / 3, Dirs: res.Dirs / 3, Bytes: res.ScannedBytes / 3,
			Current: t.Path(t.Root.kids[0]), ElapsedMs: 1000, UsedBytes: g.fs.Used, Top: []schema.ScanTop{},
		},
		schema.ScanProgress{
			T: schema.ScanLineProgress, V: schema.ScanVersion,
			Files: res.Files / 2, Dirs: res.Dirs / 2, Bytes: res.ScannedBytes / 2, NoAccessDirs: res.NoAccess.Count,
			Current: t.Path(t.Root.kids[len(t.Root.kids)-1]), ElapsedMs: 2000, UsedBytes: g.fs.Used, Top: top,
		},
		res,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// TestGoldenFixtures checks that testdata/scan-<os>.ndjson is what the
// code makes from the fake trees. Run it with -update after a change.
func TestGoldenFixtures(t *testing.T) {
	for _, g := range goldens {
		t.Run(g.os, func(t *testing.T) {
			got, err := g.scanLines()
			if err != nil {
				t.Fatal(err)
			}
			path := "../../testdata/scan-" + g.os + ".ndjson"
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s is out of date; run go test ./internal/scan -run TestGoldenFixtures -update", path)
			}
		})
	}
}
