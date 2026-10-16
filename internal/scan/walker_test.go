package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

func writeFile(t testing.TB, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func alloc(t testing.TB, p string) int64 {
	t.Helper()
	st, err := lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.alloc
}

// root returns a temp folder with no symlinks in its path.
func tempRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWalkerTotals(t *testing.T) {
	root := tempRoot(t)
	files := map[string]int{
		"a.bin":             3 << 20,
		"docs/b.txt":        10_000,
		"docs/deep/c/d.bin": 2 << 20,
		"code/go.mod":       100,
	}
	var want int64
	for p, n := range files {
		writeFile(t, filepath.Join(root, p), n)
		want += alloc(t, filepath.Join(root, p))
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "code", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	tree, err := NewWalker(Options{Root: root, KeepFileBytes: 1 << 20}).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tree.rollup()
	if tree.Root.bytes != want {
		t.Errorf("bytes = %d, want %d", tree.Root.bytes, want)
	}
	if tree.Files != 4 {
		t.Errorf("files = %d, want 4", tree.Files)
	}
	// root, docs, deep, c, code, .git, empty
	if tree.Dirs != 7 {
		t.Errorf("dirs = %d, want 7", tree.Dirs)
	}
	code := tree.find(filepath.Join(root, "code"))
	if code == nil || code.marks&markGit == 0 || code.marks&markProject == 0 {
		t.Errorf("code marks = %v, want git and project", code)
	}
	if len(tree.Root.big) != 1 || tree.Root.big[0].name != "a.bin" {
		t.Errorf("big files of root = %v, want a.bin", tree.Root.big)
	}
}

func TestWalkerLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	root := tempRoot(t)
	p := filepath.Join(root, "data", "big.bin")
	writeFile(t, p, 1<<20)
	if err := os.Link(p, filepath.Join(root, "data", "hard.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "data", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, filepath.Join(root, "soft.bin")); err != nil {
		t.Fatal(err)
	}

	tree, err := NewWalker(Options{Root: root}).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tree.rollup()
	if tree.Files != 1 {
		t.Errorf("files = %d, want 1: a hard link counts once and symlinks do not count", tree.Files)
	}
	if want := alloc(t, p); tree.Root.bytes != want {
		t.Errorf("bytes = %d, want %d", tree.Root.bytes, want)
	}
}

func TestWalkerNoAccess(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs Unix modes and a user that is not root")
	}
	root := tempRoot(t)
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "secret"), 1000)
	writeFile(t, filepath.Join(root, "open", "f"), 1000)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	tree, err := NewWalker(Options{Root: root}).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tree.NoAccessCount != 1 || len(tree.NoAccess) != 1 || tree.NoAccess[0] != locked {
		t.Errorf("no access = %v (%d), want [%s]", tree.NoAccess, tree.NoAccessCount, locked)
	}
	if tree.Files != 1 {
		t.Errorf("files = %d, want 1", tree.Files)
	}

	if _, err := NewWalker(Options{Root: locked}).Run(context.Background(), nil); err == nil {
		t.Error("walk of a root with no access: got no error")
	}
}

func TestWalkerRootNotFolder(t *testing.T) {
	root := tempRoot(t)
	writeFile(t, filepath.Join(root, "f"), 10)
	if _, err := NewWalker(Options{Root: filepath.Join(root, "f")}).Run(context.Background(), nil); err == nil {
		t.Error("walk of a file: got no error")
	}
	if _, err := NewWalker(Options{Root: filepath.Join(root, "missing")}).Run(context.Background(), nil); err == nil {
		t.Error("walk of a missing folder: got no error")
	}
}

func TestWalkerSkip(t *testing.T) {
	root := tempRoot(t)
	writeFile(t, filepath.Join(root, "keep", "f"), 1000)
	writeFile(t, filepath.Join(root, "skip", "f"), 1000)
	tree, err := NewWalker(Options{Root: root, Skip: []string{filepath.Join(root, "skip")}}).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Files != 1 || tree.find(filepath.Join(root, "skip")) != nil {
		t.Errorf("files = %d, want 1 and no skip folder", tree.Files)
	}
}

// makeTree writes n empty files in folders of 100 files.
func makeTree(t testing.TB, n int) string {
	t.Helper()
	root := tempRoot(t)
	for i := 0; i < n; i += 100 {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", i/10000), fmt.Sprintf("e%04d", i/100))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 100 && i+j < n; j++ {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("f%02d", j)))
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	return root
}

func TestWalkerCancel(t *testing.T) {
	root := makeTree(t, 30_000)
	ctx, cancel := context.WithCancel(context.Background())
	var at time.Time
	w := NewWalker(Options{Root: root, Workers: 2, ProgressInterval: time.Millisecond})
	_, err := w.Run(ctx, func(schema.ScanProgress) {
		if at.IsZero() {
			at = time.Now()
			cancel()
		}
	})
	if err == nil {
		t.Skip("the walk ended before the first progress line")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(at); d > 500*time.Millisecond {
		t.Errorf("walk stopped %v after cancel, want < 500ms", d)
	}
}

func TestWalkerProgress(t *testing.T) {
	root := makeTree(t, 3000)
	var lines []schema.ScanProgress
	w := NewWalker(Options{Root: root, ProgressInterval: time.Millisecond, UsedBytes: 42})
	if _, err := w.Run(context.Background(), func(p schema.ScanProgress) { lines = append(lines, p) }); err != nil {
		t.Fatal(err)
	}
	p := w.progress(time.Now())
	if p.T != schema.ScanLineProgress || p.Files != 3000 || p.UsedBytes != 42 || p.Top == nil {
		t.Errorf("progress = %+v", p)
	}
}

func BenchmarkWalk200k(b *testing.B) {
	root := makeTree(b, 200_000)
	b.ResetTimer()
	for range b.N {
		tree, err := NewWalker(Options{Root: root}).Run(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		if tree.Files != 200_000 {
			b.Fatalf("files = %d", tree.Files)
		}
		tree.Prune(DefaultMinBytes, DefaultMinFrac, DefaultMaxDepth)
	}
}
