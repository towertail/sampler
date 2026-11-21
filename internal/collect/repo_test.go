package collect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// statFake answers from a set of existing paths and counts the calls.
type statFake struct {
	exists map[string]bool
	calls  []string
	delay  time.Duration
	err    error
}

func (s *statFake) stat(p string) (os.FileInfo, error) {
	s.calls = append(s.calls, p)
	time.Sleep(s.delay)
	if s.err != nil {
		return nil, s.err
	}
	if s.exists[p] {
		return nil, nil
	}
	return nil, os.ErrNotExist
}

func (s *statFake) finder(budget time.Duration) *repoFinder {
	return &repoFinder{stat: s.stat, budget: budget, cache: map[string]string{}}
}

func abs(p string) string { return filepath.FromSlash(p) }

func TestRepoFinderGitDir(t *testing.T) {
	s := &statFake{exists: map[string]bool{filepath.Join(abs("/r"), ".git"): true}}
	got := s.finder(time.Second).Roots([]string{abs("/r/a/b")})
	if got[abs("/r/a/b")] != abs("/r") {
		t.Fatalf("roots = %v", got)
	}
}

func TestRepoFinderGitFile(t *testing.T) {
	// A worktree has a .git file. Stat sees it like a folder.
	s := &statFake{exists: map[string]bool{filepath.Join(abs("/wt"), ".git"): true}}
	got := s.finder(time.Second).Roots([]string{abs("/wt")})
	if got[abs("/wt")] != abs("/wt") {
		t.Fatalf("roots = %v", got)
	}
}

func TestRepoFinderNoRepo(t *testing.T) {
	s := &statFake{exists: map[string]bool{}}
	got := s.finder(time.Second).Roots([]string{abs("/a/b"), "", "relative"})
	if len(got) != 0 {
		t.Fatalf("roots = %v", got)
	}
}

func TestRepoFinderStatErrorStopsWalk(t *testing.T) {
	s := &statFake{err: os.ErrPermission}
	f := s.finder(time.Second)
	if got := f.Roots([]string{abs("/a/b/c")}); len(got) != 0 {
		t.Fatalf("roots = %v", got)
	}
	if len(s.calls) != 1 {
		t.Errorf("stat calls = %d, want 1", len(s.calls))
	}
}

func TestRepoFinderCachesParents(t *testing.T) {
	s := &statFake{exists: map[string]bool{filepath.Join(abs("/r"), ".git"): true}}
	f := s.finder(time.Second)
	got := f.Roots([]string{abs("/r/a/b"), abs("/r/a/c"), abs("/r/a/b")})
	if got[abs("/r/a/b")] != abs("/r") || got[abs("/r/a/c")] != abs("/r") {
		t.Fatalf("roots = %v", got)
	}
	// /r/a/b, /r/a, /r for the first dir; only /r/a/c for the second.
	if len(s.calls) != 4 {
		t.Errorf("stat calls = %d (%v), want 4", len(s.calls), s.calls)
	}
}

func TestRepoFinderBudget(t *testing.T) {
	s := &statFake{exists: map[string]bool{}, delay: 20 * time.Millisecond}
	dirs := []string{abs("/a"), abs("/b"), abs("/c"), abs("/d"), abs("/e"), abs("/f"), abs("/g"), abs("/h")}
	start := time.Now()
	s.finder(50 * time.Millisecond).Roots(dirs)
	if time.Since(start) > 400*time.Millisecond {
		t.Errorf("lookup took %v", time.Since(start))
	}
	// Each dir needs 2 stats (dir and /). The budget stops the loop early.
	if len(s.calls) >= 2*len(dirs) {
		t.Errorf("stat calls = %d, want fewer than %d", len(s.calls), 2*len(dirs))
	}
}
