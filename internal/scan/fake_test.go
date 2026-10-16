package scan

import (
	"path"
	"strings"
	"time"
)

// fakeFile is one file of a fake tree. The path is relative to the
// root and uses / on all OSes.
type fakeFile struct {
	path  string
	bytes int64
	mtime time.Time
}

// fakeTree makes a walked tree with no disk access, as the walker does
// for the files.
func fakeTree(goos, root string, files []fakeFile, dirs ...string) *Tree {
	t := newTree(root, goos)
	globs := RulesFor(goos).Globs()
	dir := func(rel string) *Dir {
		d := t.Root
		if rel == "." || rel == "" {
			return d
		}
		for _, seg := range strings.Split(rel, "/") {
			var next *Dir
			for _, k := range d.kids {
				if k.name == seg {
					next = k
				}
			}
			if next == nil {
				next = d.addDir(seg)
				if seg == ".git" {
					d.marks |= markGit
				}
				t.Dirs++
			}
			d = next
		}
		return d
	}
	for _, p := range dirs {
		dir(p)
	}
	for _, f := range files {
		d := dir(path.Dir(f.path))
		name := path.Base(f.path)
		if projectMarkers[name] {
			d.marks |= markProject
		}
		d.own += f.bytes
		d.ownFiles++
		if m := f.mtime.Unix(); m > d.newest {
			d.newest = m
		}
		if f.bytes >= 1<<20 {
			d.big = append(d.big, File{name: name, bytes: f.bytes})
		}
		for _, g := range globs {
			if ok, _ := path.Match(g, name); ok {
				if d.globs == nil {
					d.globs = map[string]int64{}
				}
				d.globs[g] += f.bytes
			}
		}
		t.Files++
	}
	t.Dirs++
	return t
}
