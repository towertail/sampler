package scan

import (
	"sort"
	"strings"

	"github.com/towertail/sampler/pkg/schema"
)

// Project markers that a walk records on a folder.
const (
	markGit uint8 = 1 << iota
	markProject
)

// Dir is one folder of the full tree. One walker goroutine fills a Dir.
// After the walk, rollup sets the totals.
type Dir struct {
	name     string
	parent   *Dir
	depth    int
	top      int
	kids     []*Dir
	own      int64
	ownFiles int64
	big      []File
	globs    map[string]int64
	newest   int64
	marks    uint8
	noAccess bool

	bytes  int64
	files  int64
	latest int64
}

// File is a file that is large enough to show as its own node.
type File struct {
	name  string
	bytes int64
}

// Tree is the result of a walk.
type Tree struct {
	Root     *Dir
	RootPath string
	OS       string
	sep      string

	Files         int64
	Dirs          int64
	NoAccess      []string
	NoAccessCount int64

	pruned *node
	byPath map[string]*node
}

// node is one node of the pruned tree.
type node struct {
	name  string
	paths []string
	dir   *Dir
	bytes int64
	cat   string
	id    string
	file  bool
	other bool
	kids  []*node
}

func newTree(root, goos string) *Tree {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	return &Tree{
		Root:     &Dir{name: root, top: -1},
		RootPath: root,
		OS:       goos,
		sep:      sep,
	}
}

func (d *Dir) addDir(name string) *Dir {
	k := &Dir{name: name, parent: d, depth: d.depth + 1, top: d.top}
	d.kids = append(d.kids, k)
	return k
}

// Path returns the full path of d.
func (t *Tree) Path(d *Dir) string {
	if d.parent == nil {
		return t.RootPath
	}
	return t.join(t.Path(d.parent), d.name)
}

// join adds a name to a folder path. The root path can end with the
// separator, as / and C:\ do.
func (t *Tree) join(dir, name string) string {
	return strings.TrimSuffix(dir, t.sep) + t.sep + name
}

// rollup sets the totals of each folder from the bottom up.
func (t *Tree) rollup() {
	var walk func(d *Dir)
	walk = func(d *Dir) {
		d.bytes, d.files, d.latest = d.own, d.ownFiles, d.newest
		for _, k := range d.kids {
			walk(k)
			d.bytes += k.bytes
			d.files += k.files
			if k.latest > d.latest {
				d.latest = k.latest
			}
		}
	}
	walk(t.Root)
}

// Prune makes the pruned tree. A node smaller than both minBytes and
// minFrac of the root goes into the "other" node of its parent. A
// minFrac of 0 sets no fraction floor. Nodes deeper than maxDepth have
// no children. Prune returns the effective floor.
func (t *Tree) Prune(minBytes int64, minFrac float64, maxDepth int) int64 {
	t.rollup()
	limit := minBytes
	if f := int64(minFrac * float64(t.Root.bytes)); minFrac > 0 && f < limit {
		limit = f
	}
	if limit < 1 {
		limit = 1
	}
	t.byPath = map[string]*node{}
	t.pruned = t.pruneDir(t.Root, t.RootPath, limit, maxDepth)
	return limit
}

func (t *Tree) pruneDir(d *Dir, path string, limit int64, maxDepth int) *node {
	n := &node{name: d.name, paths: []string{path}, dir: d, bytes: d.bytes}
	if d.parent == nil {
		n.name = path
	}
	t.byPath[path] = n
	if d.depth >= maxDepth {
		return n
	}
	rest := d.own
	for _, f := range d.big {
		if f.bytes >= limit {
			n.kids = append(n.kids, &node{name: f.name, paths: []string{t.join(path, f.name)}, bytes: f.bytes, file: true})
			rest -= f.bytes
		}
	}
	for _, k := range d.kids {
		if k.bytes >= limit && k.bytes > 0 {
			n.kids = append(n.kids, t.pruneDir(k, t.join(path, k.name), limit, maxDepth))
		} else {
			rest += k.bytes
		}
	}
	if len(n.kids) == 0 {
		return n
	}
	sort.SliceStable(n.kids, func(i, j int) bool { return n.kids[i].bytes > n.kids[j].bytes })
	if rest > 0 {
		n.kids = append(n.kids, &node{name: "other", bytes: rest, other: true})
	}
	// Join a chain of folders that each have one child, as in a/b/c.
	if d.parent != nil && len(n.kids) == 1 && n.kids[0].dir != nil {
		k := n.kids[0]
		n.name += t.sep + k.name
		n.paths = append(n.paths, k.paths...)
		for _, p := range k.paths {
			t.byPath[p] = n
		}
		n.dir, n.kids = k.dir, k.kids
	}
	return n
}

// Schema returns the pruned tree. Call Prune first.
func (t *Tree) Schema() schema.ScanNode {
	return t.pruned.schema()
}

func (n *node) schema() schema.ScanNode {
	out := schema.ScanNode{N: n.name, B: n.bytes, Cat: n.cat, ID: n.id, F: n.file, O: n.other}
	if out.Cat == "" {
		out.Cat = schema.ScanCatOther
	}
	for _, k := range n.kids {
		out.C = append(out.C, k.schema())
	}
	return out
}

// find returns the full-tree folder at path, or nil.
func (t *Tree) find(path string) *Dir {
	rel, ok := t.rel(t.RootPath, path)
	if !ok {
		return nil
	}
	d := t.Root
	if rel == "" {
		return d
	}
	for _, seg := range strings.Split(rel, t.sep) {
		var next *Dir
		for _, k := range d.kids {
			if t.same(k.name, seg) {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		d = next
	}
	return d
}

// rel returns path relative to base, and false when path is not base
// or below it.
func (t *Tree) rel(base, path string) (string, bool) {
	base = strings.TrimSuffix(base, t.sep)
	if t.same(path, base) {
		return "", true
	}
	if len(path) <= len(base) || !t.same(path[:len(base)], base) || path[len(base):len(base)+len(t.sep)] != t.sep {
		return "", false
	}
	return path[len(base)+len(t.sep):], true
}

// same compares two paths the way the host OS does.
func (t *Tree) same(a, b string) bool {
	if t.OS == "windows" || t.OS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
