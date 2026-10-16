package scan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// Group is the group of a recommendation.
type Group string

const (
	Safe   Group = schema.ScanGroupSafe
	Review Group = schema.ScanGroupReview
)

// Hit is one match of a rule. Each hit gives one recommendation.
type Hit struct {
	// Targets are what the commands remove.
	Targets []Target
	// Nodes are the full paths of the tree nodes that the hit covers.
	Nodes []string
	Bytes int64
	// Label replaces the label of the rule when it is set.
	Label string
}

// Target is one folder to remove, or the files in a folder that match
// a glob.
type Target struct {
	Path string
	// Glob is a file name pattern, for example *.deb. Empty means the
	// folder itself.
	Glob string
}

// Matcher finds the hits of a rule in a tree.
type Matcher func(m *Match) []Hit

// Rule is one cleanup rule.
type Rule struct {
	ID       string
	Label    string
	Group    Group
	Match    Matcher
	Elevated bool
	// Globs are the file name patterns that Match reads with
	// Match.GlobBytes. The walk adds them up.
	Globs []string
}

// RuleSet is the rules for one OS and the paths that no rule can touch.
type RuleSet struct {
	OS    string
	Rules []Rule
	// Never holds paths. A target in or above one of them is dropped.
	Never []string
}

// minRecBytes is the smallest recommendation.
const minRecBytes = 10_000_000

// Match is what a Matcher gets.
type Match struct {
	t    *Tree
	home string
	now  time.Time
}

// RulesFor returns the rule set of an OS.
func RulesFor(goos string) RuleSet {
	switch goos {
	case "darwin":
		return darwinRules
	case "linux":
		return linuxRules
	case "windows":
		return windowsRules
	}
	return RuleSet{OS: goos}
}

// Globs returns the file name patterns that the rules need.
func (r RuleSet) Globs() []string {
	seen := map[string]bool{}
	var out []string
	for _, rule := range r.Rules {
		for _, g := range rule.Globs {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// Apply finds the recommendations, sets the ids of the nodes that they
// point to, and returns them, safe ones first and the largest first in
// each group. Call Prune first.
func (r RuleSet) Apply(t *Tree, home string, now time.Time) []schema.ScanRec {
	m := &Match{t: t, home: home, now: now}
	sh := Shell{OS: t.OS, Home: home, sep: t.sep}
	never := make([]string, 0, len(r.Never))
	for _, p := range r.Never {
		never = append(never, t.expand(p, home))
	}
	recs := []schema.ScanRec{}
	used := map[string]int{}
	nodeN := 0
	for _, rule := range r.Rules {
		for _, h := range rule.Match(m) {
			if h.Bytes < minRecBytes || len(h.Targets) == 0 || r.blocked(t, never, h.Targets) {
				continue
			}
			id := rule.ID
			if used[id]++; used[id] > 1 {
				id = fmt.Sprintf("%s-%d", rule.ID, used[id])
			}
			label := rule.Label
			if h.Label != "" {
				label = h.Label
			}
			cmds := sh.Remove(h.Targets, rule.Elevated)
			rec := schema.ScanRec{
				ID:       id,
				Group:    string(rule.Group),
				Label:    label,
				Path:     sh.Display(h.Targets),
				Bytes:    h.Bytes,
				Elevated: rule.Elevated,
				NodeIDs:  []string{},
				Cmd:      schema.ScanCmd{Sh: []string{}, Pwsh: []string{}},
			}
			if t.OS == "windows" {
				rec.Cmd.Pwsh = cmds
			} else {
				rec.Cmd.Sh = cmds
			}
			seen := map[string]bool{}
			for _, p := range h.Nodes {
				n := t.byPath[p]
				if n == nil {
					continue
				}
				if n.id == "" {
					nodeN++
					n.id = fmt.Sprintf("n%d", nodeN)
				}
				if !seen[n.id] {
					seen[n.id] = true
					rec.NodeIDs = append(rec.NodeIDs, n.id)
				}
			}
			recs = append(recs, rec)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Group != recs[j].Group {
			return recs[i].Group == schema.ScanGroupSafe
		}
		return recs[i].Bytes > recs[j].Bytes
	})
	return recs
}

// blocked reports whether a target is outside the scan root, or in or
// above a path of the never list.
func (r RuleSet) blocked(t *Tree, never []string, targets []Target) bool {
	for _, tg := range targets {
		p := tg.Path
		if _, ok := t.rel(t.RootPath, p); !ok {
			return true
		}
		for _, n := range never {
			if _, ok := t.rel(n, p); ok {
				return true
			}
			if _, ok := t.rel(p, n); ok {
				return true
			}
		}
	}
	return false
}

// Dir returns the folder at a rule path, or nil when the scan did not
// find it.
func (m *Match) Dir(p string) *Dir {
	return m.t.find(m.t.expand(p, m.home))
}

// Path returns the full path of a folder.
func (m *Match) Path(d *Dir) string { return m.t.Path(d) }

// GlobBytes returns the size of the files of d that match glob.
func (m *Match) GlobBytes(d *Dir, glob string) int64 { return d.globs[glob] }

// wholeDir matches one folder and removes it.
func wholeDir(p string) Matcher {
	return func(m *Match) []Hit {
		d := m.Dir(p)
		if d == nil || d.bytes == 0 {
			return nil
		}
		full := m.Path(d)
		return []Hit{{Targets: []Target{{Path: full}}, Nodes: []string{full}, Bytes: d.bytes}}
	}
}

// anyDir matches the first folder in paths that the scan found.
func anyDir(paths ...string) Matcher {
	return func(m *Match) []Hit {
		for _, p := range paths {
			if h := wholeDir(p)(m); h != nil {
				return h
			}
		}
		return nil
	}
}

// dirContents matches a folder and removes what is in it, but not the
// folder.
func dirContents(p string) Matcher {
	return func(m *Match) []Hit {
		d := m.Dir(p)
		if d == nil || d.bytes == 0 {
			return nil
		}
		full := m.Path(d)
		return []Hit{{Targets: []Target{{Path: full, Glob: "*"}}, Nodes: []string{full}, Bytes: d.bytes}}
	}
}

// dirGlobs matches the files of a folder that match the globs.
func dirGlobs(p string, globs ...string) Matcher {
	return func(m *Match) []Hit {
		d := m.Dir(p)
		if d == nil {
			return nil
		}
		full := m.Path(d)
		h := Hit{Nodes: []string{full}}
		for _, g := range globs {
			if b := m.GlobBytes(d, g); b > 0 {
				h.Bytes += b
				h.Targets = append(h.Targets, Target{Path: full, Glob: g})
			}
		}
		if h.Bytes == 0 {
			return nil
		}
		return []Hit{h}
	}
}

// subtreeGlob matches the files that match glob in each folder below p.
// It gives one hit with a target for each folder that has such files.
func subtreeGlob(p, glob string) Matcher {
	return func(m *Match) []Hit {
		d := m.Dir(p)
		if d == nil {
			return nil
		}
		var h Hit
		var walk func(d *Dir)
		walk = func(d *Dir) {
			if b := m.GlobBytes(d, glob); b > 0 {
				full := m.Path(d)
				h.Bytes += b
				h.Targets = append(h.Targets, Target{Path: full, Glob: glob})
				h.Nodes = append(h.Nodes, full)
			}
			for _, k := range d.kids {
				walk(k)
			}
		}
		walk(d)
		if h.Bytes == 0 {
			return nil
		}
		sort.Slice(h.Targets, func(i, j int) bool { return h.Targets[i].Path < h.Targets[j].Path })
		return []Hit{h}
	}
}

// datedName matches folder names that hold a date or a time stamp, as
// release tools make them: 20260512-0931, 2026-05-12, 20260512093100.
var datedName = regexp.MustCompile(`^(20\d{2}-?[01]\d-?[0-3]\d([-_T]?\d{4,6})?|\d{10,14})$`)

// keepReleases is the number of newest dated folders that stay.
const keepReleases = 2

// oldReleases matches folders named "releases" that hold minDated or
// more dated folders, as deploy tools make them. It removes all of them
// but the newest two.
func oldReleases(minDated int) Matcher {
	return func(m *Match) []Hit {
		var hits []Hit
		var walk func(d *Dir)
		walk = func(d *Dir) {
			var dated []*Dir
			if d.name == "releases" {
				for _, k := range d.kids {
					if datedName.MatchString(k.name) {
						dated = append(dated, k)
					}
				}
			}
			if len(dated) >= minDated {
				sort.Slice(dated, func(i, j int) bool { return dated[i].name < dated[j].name })
				var h Hit
				for _, k := range dated[:len(dated)-keepReleases] {
					full := m.Path(k)
					h.Targets = append(h.Targets, Target{Path: full})
					h.Nodes = append(h.Nodes, full)
					h.Bytes += k.bytes
				}
				h.Label = fmt.Sprintf("Old releases in %s (%d)", m.Path(d.parent), len(h.Targets))
				hits = append(hits, h)
				return
			}
			for _, k := range d.kids {
				if !packageDir(k.name) {
					walk(k)
				}
			}
		}
		walk(m.t.Root)
		return hits
	}
}

// packageDir reports whether a folder holds packages of a tool or an
// app bundle. The matchers do not look inside it.
func packageDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "site-packages", ".git":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".app")
}

// staleNodeModules matches node_modules folders of projects that did
// not change for the given time.
func staleNodeModules(age time.Duration) Matcher {
	return func(m *Match) []Hit {
		limit := m.now.Add(-age).Unix()
		var h Hit
		var walk func(d *Dir)
		walk = func(d *Dir) {
			for _, k := range d.kids {
				if k.name != "node_modules" {
					continue
				}
				if d.marks&markProject != 0 && k.bytes > 0 && newestOutside(d, k) < limit {
					full := m.Path(k)
					h.Targets = append(h.Targets, Target{Path: full})
					h.Nodes = append(h.Nodes, full)
					h.Bytes += k.bytes
				}
			}
			for _, k := range d.kids {
				if !packageDir(k.name) {
					walk(k)
				}
			}
		}
		walk(m.t.Root)
		if h.Bytes == 0 {
			return nil
		}
		h.Label = fmt.Sprintf("node_modules in %d old projects", len(h.Targets))
		if len(h.Targets) == 1 {
			h.Label = "node_modules in 1 old project"
		}
		return []Hit{h}
	}
}

// newestOutside returns the newest change time below d, not below skip.
func newestOutside(d, skip *Dir) int64 {
	n := d.newest
	for _, k := range d.kids {
		if k == skip {
			continue
		}
		if k.latest > n {
			n = k.latest
		}
	}
	return n
}

// Shell writes commands for the shell of the host.
type Shell struct {
	OS   string
	Home string
	sep  string
}

// Remove returns one remove command for each target.
func (s Shell) Remove(targets []Target, elevated bool) []string {
	out := make([]string, 0, len(targets))
	for _, tg := range targets {
		dir, glob := tg.Path, tg.Glob
		if s.OS == "windows" {
			out = append(out, s.pwshRemove(dir, glob, elevated))
			continue
		}
		cmd := "rm -rf -- " + s.shQuote(dir)
		if glob != "" {
			cmd = "rm -rf -- " + s.shQuote(dir) + "/" + glob
		}
		if elevated {
			cmd = "sudo " + cmd
		}
		out = append(out, cmd)
	}
	return out
}

func (s Shell) pwshRemove(dir, glob string, elevated bool) string {
	var cmd string
	if glob != "" {
		cmd = "Remove-Item -Path " + s.pwshQuote(dir, `\`+glob) + " -Recurse -Force -ErrorAction SilentlyContinue"
	} else {
		cmd = "Remove-Item -LiteralPath " + s.pwshQuote(dir, "") + " -Recurse -Force"
	}
	if elevated {
		cmd += "  # admin"
	}
	return cmd
}

// rel returns p relative to the home folder.
func (s Shell) rel(p string) (string, bool) {
	t := Tree{OS: s.OS, sep: s.sep}
	if s.Home == "" {
		return "", false
	}
	return t.rel(s.Home, p)
}

// safeSh is the set of characters that need no quotes in sh.
var safeSh = regexp.MustCompile(`^[A-Za-z0-9._/@%+=:,~-]+$`)

// shQuote quotes a path for bash and zsh. A path below the home folder
// starts with $HOME. Parts with other characters than safeSh go in
// single quotes, so that an interactive shell does not expand ! or $.
func (s Shell) shQuote(p string) string {
	if rel, ok := s.rel(p); ok {
		if rel == "" {
			return `"$HOME"`
		}
		if safeSh.MatchString(rel) {
			return `"$HOME/` + rel + `"`
		}
		return `"$HOME"/` + shSingle(rel)
	}
	if safeSh.MatchString(p) {
		return p
	}
	return shSingle(p)
}

func shSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pwshQuote quotes a path for PowerShell in double quotes. A path below
// the home folder starts with $env:USERPROFILE. A glob goes in the
// quotes after the path, and then the wildcard characters of the path
// are escaped for -Path.
func (s Shell) pwshQuote(p, glob string) string {
	wild := glob != ""
	esc := func(x string) string {
		var b strings.Builder
		for _, r := range x {
			switch r {
			case '`', '$', '"', '“', '”', '„':
				b.WriteRune('`')
			case '[', ']':
				if wild {
					b.WriteString("``")
				}
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	if rel, ok := s.rel(p); ok {
		if rel == "" {
			return `"$env:USERPROFILE` + glob + `"`
		}
		return `"$env:USERPROFILE\` + esc(rel) + glob + `"`
	}
	return `"` + esc(p) + glob + `"`
}

// Display returns the path text of a recommendation.
func (s Shell) Display(targets []Target) string {
	home := "~"
	if s.OS == "windows" {
		home = "%USERPROFILE%"
	}
	parts := make([]string, 0, len(targets))
	for _, tg := range targets {
		p := tg.Path
		if tg.Glob != "" {
			p = strings.TrimSuffix(p, s.sep) + s.sep + tg.Glob
		}
		if rel, ok := s.rel(p); ok {
			p = home
			if rel != "" {
				p += s.sep + rel
			}
		}
		parts = append(parts, p)
	}
	if len(parts) > 3 {
		return strings.Join(parts[:2], ", ") + fmt.Sprintf(" +%d", len(parts)-2)
	}
	return strings.Join(parts, ", ")
}
