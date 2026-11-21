package collect

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// repoBudget is the time that one scan may spend on repo lookups.
const repoBudget = 100 * time.Millisecond

// repoFinder finds the repo root of a folder: the nearest folder that
// holds a .git entry. One finder serves one scan, so the cache lives as
// long as the scan.
type repoFinder struct {
	stat   func(string) (os.FileInfo, error)
	budget time.Duration
	// cache maps a folder to its repo root. An empty root means no repo.
	cache map[string]string
}

func newRepoFinder(budget time.Duration) *repoFinder {
	return &repoFinder{stat: os.Stat, budget: budget, cache: map[string]string{}}
}

// Roots returns the repo root of each folder in dirs that has one. It
// returns what it has found when the budget ends.
func (f *repoFinder) Roots(dirs []string) map[string]string {
	out := map[string]string{}
	deadline := time.Now().Add(f.budget)
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		root, ok := f.find(dir, deadline)
		if !ok {
			break
		}
		if root != "" {
			out[dir] = root
		}
	}
	return out
}

// find walks up from dir. It returns false when the budget ends. A stat
// error other than "not exist" ends the walk with no repo.
func (f *repoFinder) find(dir string, deadline time.Time) (string, bool) {
	var seen []string
	root := ""
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if r, ok := f.cache[d]; ok {
			root = r
			break
		}
		if time.Now().After(deadline) {
			return "", false
		}
		_, err := f.stat(filepath.Join(d, ".git"))
		if err == nil {
			root = d
			seen = append(seen, d)
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", true
		}
		seen = append(seen, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	for _, d := range seen {
		f.cache[d] = root
	}
	return root, true
}
