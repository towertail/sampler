package scan

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// maxNoAccessPaths is the limit of no_access.dirs in the result.
const maxNoAccessPaths = 50

// Options configure a walk.
type Options struct {
	Root string
	// OS is the GOOS of the host. Tests set it.
	OS string
	// KeepFileBytes is the size from which a file is kept as its own
	// node in the full tree.
	KeepFileBytes int64
	// Globs are file name patterns. The walk adds the size of the
	// matching files of each folder for each pattern.
	Globs            []string
	Workers          int
	ProgressInterval time.Duration
	// UsedBytes goes into each progress line.
	UsedBytes int64
	// Skip holds full paths that the walk does not enter.
	Skip []string
	// Devices holds the devices that the walk can enter, in addition
	// to the device of the root.
	Devices []uint64
}

// Walker walks a folder tree with a bounded pool of goroutines.
type Walker struct {
	opts Options
	tree *Tree

	mu       sync.Mutex
	queue    []*Dir
	pending  int
	wake     *sync.Cond
	links    map[[2]uint64]struct{}
	devices  map[uint64]bool
	skip     map[string]bool
	current  atomic.Value
	files    atomic.Int64
	dirs     atomic.Int64
	bytes    atomic.Int64
	noAccess atomic.Int64
	tops     []atomic.Int64
}

// NewWalker returns a Walker for opts.
func NewWalker(opts Options) *Walker {
	if opts.OS == "" {
		opts.OS = runtime.GOOS
	}
	if opts.Workers <= 0 {
		opts.Workers = min(8, runtime.GOMAXPROCS(0)*2)
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = time.Second
	}
	w := &Walker{
		opts:    opts,
		tree:    newTree(opts.Root, opts.OS),
		links:   map[[2]uint64]struct{}{},
		devices: map[uint64]bool{},
		skip:    map[string]bool{},
	}
	for _, d := range opts.Devices {
		w.devices[d] = true
	}
	for _, p := range opts.Skip {
		w.skip[p] = true
	}
	w.wake = sync.NewCond(&w.mu)
	w.current.Store(opts.Root)
	return w
}

// Run walks the tree. It returns ctx.Err() when ctx ends first.
func (w *Walker) Run(ctx context.Context, progress func(schema.ScanProgress)) (*Tree, error) {
	start := time.Now()
	st, err := lstat(w.opts.Root)
	if err != nil {
		return nil, err
	}
	if !st.dir {
		return nil, errors.New("root is not a folder: " + w.opts.Root)
	}
	w.devices[st.dev] = true

	root := w.tree.Root
	w.read(ctx, root, w.opts.Root)
	if root.noAccess {
		return nil, errors.New("no access to " + w.opts.Root)
	}
	w.tops = make([]atomic.Int64, len(root.kids))
	for i, k := range root.kids {
		k.top = i
	}
	w.queue = append(w.queue, root.kids...)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	var tick sync.WaitGroup
	if progress != nil {
		tick.Add(1)
		go func() {
			defer tick.Done()
			t := time.NewTicker(w.opts.ProgressInterval)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					progress(w.progress(start))
				}
			}
		}()
	}
	go func() {
		<-ctx.Done()
		w.mu.Lock()
		w.wake.Broadcast()
		w.mu.Unlock()
	}()

	var wg sync.WaitGroup
	for range w.opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.work(ctx)
		}()
	}
	wg.Wait()
	close(stop)
	tick.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t := w.tree
	t.Files = w.files.Load()
	t.Dirs = w.dirs.Load()
	t.NoAccessCount = w.noAccess.Load()
	sort.Strings(t.NoAccess)
	return t, nil
}

// work takes folders from the queue until the queue is empty and no
// worker reads a folder.
func (w *Walker) work(ctx context.Context) {
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && w.pending > 0 && ctx.Err() == nil {
			w.wake.Wait()
		}
		if ctx.Err() != nil || len(w.queue) == 0 {
			w.wake.Broadcast()
			w.mu.Unlock()
			return
		}
		d := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.pending++
		w.mu.Unlock()

		w.read(ctx, d, w.tree.Path(d))

		w.mu.Lock()
		w.pending--
		w.queue = append(w.queue, d.kids...)
		w.wake.Broadcast()
		w.mu.Unlock()
	}
}

// read reads one folder. It adds the subfolders to d.kids. The caller
// queues them.
func (w *Walker) read(ctx context.Context, d *Dir, dirPath string) {
	w.dirs.Add(1)
	w.current.Store(dirPath)
	f, err := os.Open(dirPath)
	if err != nil {
		w.denied(d, dirPath, err)
		return
	}
	defer f.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		entries, err := f.ReadDir(256)
		for _, e := range entries {
			w.entry(d, dirPath, e)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				w.denied(d, dirPath, err)
			}
			return
		}
	}
}

func (w *Walker) entry(d *Dir, dirPath string, e fs.DirEntry) {
	name := e.Name()
	full := dirPath + w.tree.sep + name
	if d.parent == nil {
		full = filepath.Join(dirPath, name)
	}
	typ := e.Type()
	if typ&fs.ModeSymlink != 0 || typ&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0 {
		return
	}
	if typ.IsDir() {
		if w.skip[full] {
			return
		}
		st, err := lstat(full)
		if err != nil || !st.dir {
			return
		}
		if !w.devices[st.dev] {
			return
		}
		d.addDir(name)
		if name == ".git" {
			d.marks |= markGit
		}
		return
	}
	st, err := lstat(full)
	if err != nil || st.dir || !st.regular {
		return
	}
	if st.nlink > 1 {
		key := [2]uint64{st.dev, st.ino}
		w.mu.Lock()
		_, seen := w.links[key]
		w.links[key] = struct{}{}
		w.mu.Unlock()
		if seen {
			return
		}
	}
	if projectMarkers[name] {
		d.marks |= markProject
	}
	d.own += st.alloc
	d.ownFiles++
	if st.mtime > d.newest {
		d.newest = st.mtime
	}
	if st.alloc >= w.opts.KeepFileBytes && w.opts.KeepFileBytes > 0 {
		d.big = append(d.big, File{name: name, bytes: st.alloc})
	}
	for _, g := range w.opts.Globs {
		if ok, _ := path.Match(g, name); ok {
			if d.globs == nil {
				d.globs = map[string]int64{}
			}
			d.globs[g] += st.alloc
		}
	}
	w.files.Add(1)
	w.bytes.Add(st.alloc)
	if d.top >= 0 {
		w.tops[d.top].Add(st.alloc)
	}
}

// denied records a folder that the walk cannot read.
func (w *Walker) denied(d *Dir, dirPath string, err error) {
	if !errors.Is(err, fs.ErrPermission) {
		return
	}
	d.noAccess = true
	w.noAccess.Add(1)
	w.mu.Lock()
	if len(w.tree.NoAccess) < maxNoAccessPaths {
		w.tree.NoAccess = append(w.tree.NoAccess, dirPath)
	}
	w.mu.Unlock()
}

func (w *Walker) progress(start time.Time) schema.ScanProgress {
	p := schema.ScanProgress{
		T:            schema.ScanLineProgress,
		V:            schema.ScanVersion,
		Files:        w.files.Load(),
		Dirs:         w.dirs.Load(),
		Bytes:        w.bytes.Load(),
		NoAccessDirs: w.noAccess.Load(),
		Current:      w.current.Load().(string),
		ElapsedMs:    time.Since(start).Milliseconds(),
		UsedBytes:    w.opts.UsedBytes,
		Top:          []schema.ScanTop{},
	}
	root := w.tree.Root
	for i, k := range root.kids {
		if b := w.tops[i].Load(); b > 0 {
			p.Top = append(p.Top, schema.ScanTop{Path: w.tree.Path(k), B: b})
		}
	}
	sort.Slice(p.Top, func(i, j int) bool { return p.Top[i].B > p.Top[j].B })
	if len(p.Top) > 5 {
		p.Top = p.Top[:5]
	}
	return p
}

// projectMarkers are file names that mark a folder as a code project.
var projectMarkers = map[string]bool{
	"package.json":     true,
	"go.mod":           true,
	"Cargo.toml":       true,
	"pyproject.toml":   true,
	"requirements.txt": true,
	"pom.xml":          true,
	"build.gradle":     true,
	"build.gradle.kts": true,
	"Gemfile":          true,
	"composer.json":    true,
	"Package.swift":    true,
	"Makefile":         true,
	"CMakeLists.txt":   true,
}
