package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// awkward are folder names that need quotes in a shell.
var awkward = []string{
	"plain",
	"with space",
	"it's",
	`say "hi"`,
	"$HOME",
	"$(touch pwned)",
	"`touch pwned`",
	"bang!",
	"star*",
	"[abc]",
	"-rf",
	"new\nline",
	"tab\there",
	"ünïcødé “curly”",
	"semi;colon&amp|pipe",
	"back\\slash",
}

// TestShRemove runs each command in bash. It checks that the command
// removes the target and nothing else.
func TestShRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	base := tempRoot(t)
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "out side")
	sh := Shell{OS: "linux", Home: home, sep: "/"}
	for _, name := range awkward {
		for _, parent := range []string{home, filepath.Join(home, "sub dir"), outside} {
			target := filepath.Join(parent, name)
			writeFile(t, filepath.Join(target, "f"), 10)
			writeFile(t, filepath.Join(parent, "keep"), 10)
			cmd := sh.Remove([]Target{{Path: target}}, false)[0]
			if out, err := exec.Command(bash, "-n", "-c", cmd).CombinedOutput(); err != nil {
				t.Fatalf("bash -n %q: %v %s", cmd, err, out)
			}
			c := exec.Command(bash, "-c", cmd)
			c.Dir = base
			c.Env = append(os.Environ(), "HOME="+home)
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("%q: %v %s", cmd, err, out)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("%q did not remove %q", cmd, target)
			}
			if _, err := os.Lstat(filepath.Join(parent, "keep")); err != nil {
				t.Errorf("%q removed a sibling: %v", cmd, err)
			}
			if _, err := os.Lstat(filepath.Join(base, "pwned")); err == nil {
				t.Fatalf("%q ran a command from the path", cmd)
			}
		}
	}
}

func TestShRemoveGlob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	base := tempRoot(t)
	home := filepath.Join(base, "home")
	dir := filepath.Join(home, "Down loads")
	writeFile(t, filepath.Join(dir, "a.dmg"), 10)
	writeFile(t, filepath.Join(dir, "b c.dmg"), 10)
	writeFile(t, filepath.Join(dir, "keep.pdf"), 10)
	sh := Shell{OS: "darwin", Home: home, sep: "/"}
	cmd := sh.Remove([]Target{{Path: dir, Glob: "*.dmg"}}, false)[0]
	if want := `rm -rf -- "$HOME"/'Down loads'/*.dmg`; cmd != want {
		t.Errorf("cmd = %s, want %s", cmd, want)
	}
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "HOME="+home)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 1 || filepath.Base(left[0]) != "keep.pdf" {
		t.Errorf("left = %v, want keep.pdf", left)
	}
}

func TestShQuote(t *testing.T) {
	sh := Shell{OS: "linux", Home: "/home/dev", sep: "/"}
	cases := map[string]string{
		"/home/dev":             `"$HOME"`,
		"/home/dev/.cache/pip":  `"$HOME/.cache/pip"`,
		"/home/dev/a b":         `"$HOME"/'a b'`,
		"/home/dev/it's":        `"$HOME"/'it'\''s'`,
		"/home/devx/a":          `/home/devx/a`,
		"/var/cache/apt":        `/var/cache/apt`,
		"/srv/a $b":             `'/srv/a $b'`,
		"/home/dev/$(touch x)":  `"$HOME"/'$(touch x)'`,
		"/home/dev/bang!":       `"$HOME"/'bang!'`,
		"/home/dev/~tilde/x":    `"$HOME/~tilde/x"`,
		"/home/dev/a,b:c@d%e+f": `"$HOME/a,b:c@d%e+f"`,
	}
	for in, want := range cases {
		if got := sh.shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := sh.Remove([]Target{{Path: "/var/crash", Glob: "*"}}, true)[0]; got != "sudo rm -rf -- /var/crash/*" {
		t.Errorf("elevated = %s", got)
	}
}

// TestPwshRemove runs each command in PowerShell with a stub
// Remove-Item that prints the path it gets.
func TestPwshRemove(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("no pwsh")
	}
	home := `C:\Users\dev`
	sh := Shell{OS: "windows", Home: home, sep: `\`}
	type tc struct {
		target Target
		want   string
	}
	var cases []tc
	for _, name := range awkward {
		if strings.ContainsAny(name, "\n\t") {
			continue
		}
		name = strings.ReplaceAll(name, `\`, "_")
		cases = append(cases,
			tc{Target{Path: home + `\` + name}, "L:" + home + `\` + name},
			tc{Target{Path: `D:\data\` + name}, `L:D:\data\` + name},
		)
	}
	cases = append(cases,
		tc{Target{Path: home + `\AppData\Local\Temp`, Glob: "*"}, `P:` + home + `\AppData\Local\Temp\*`},
		tc{Target{Path: home + `\a[1]`, Glob: "*.msi"}, "P:" + home + "\\a`[1`]\\*.msi"},
	)
	var script strings.Builder
	script.WriteString("$ErrorActionPreference = 'Stop'\n")
	script.WriteString("$env:USERPROFILE = '" + home + "'\n")
	script.WriteString("function Remove-Item { param($LiteralPath, $Path, [switch]$Recurse, [switch]$Force, $ErrorAction)\n")
	script.WriteString("  if ($LiteralPath) { 'L:' + $LiteralPath } else { 'P:' + $Path } }\n")
	for _, c := range cases {
		script.WriteString(sh.Remove([]Target{c.target}, true)[0] + "\n")
	}
	f := filepath.Join(t.TempDir(), "t.ps1")
	if err := os.WriteFile(f, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-File", f).CombinedOutput()
	if err != nil {
		t.Fatalf("pwsh: %v\n%s\n%s", err, out, script.String())
	}
	got := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r", ""), "\n"), "\n")
	if len(got) != len(cases) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(cases), out)
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%+v: got %q, want %q", c.target, got[i], c.want)
		}
	}
}

func TestDisplay(t *testing.T) {
	sh := Shell{OS: "linux", Home: "/home/dev", sep: "/"}
	got := sh.Display([]Target{{Path: "/home/dev/a"}, {Path: "/home/dev/Downloads", Glob: "*.deb"}, {Path: "/var/x"}, {Path: "/var/y"}})
	if want := "~/a, ~/Downloads/*.deb +2"; got != want {
		t.Errorf("Display = %q, want %q", got, want)
	}
	win := Shell{OS: "windows", Home: `C:\Users\dev`, sep: `\`}
	if got, want := win.Display([]Target{{Path: `C:\Users\dev\AppData\Local\Temp`, Glob: "*"}}), `%USERPROFILE%\AppData\Local\Temp\*`; got != want {
		t.Errorf("Display = %q, want %q", got, want)
	}
}

func TestNeverList(t *testing.T) {
	files := []fakeFile{
		{"home/dev/.cache/huge/x", 500 * mb, fresh},
		{"home/dev/.cache/huge/docker/y", 500 * mb, fresh},
		{"home/dev/keep/inner/z", 500 * mb, fresh},
	}
	tree := fakeTree("linux", "/", files)
	tree.Prune(DefaultMinBytes, DefaultMinFrac, DefaultMaxDepth)
	rs := RuleSet{
		OS: "linux",
		Rules: []Rule{
			{ID: "above", Group: Safe, Match: wholeDir("~/.cache/huge")},
			{ID: "inside", Group: Safe, Match: wholeDir("~/keep/inner")},
			{ID: "ok", Group: Safe, Match: wholeDir("~/.cache")},
		},
		Never: []string{"~/.cache/huge/docker", "~/keep"},
	}
	recs := rs.Apply(tree, "/home/dev", goldenNow)
	if len(recs) != 0 {
		t.Errorf("recs = %+v, want none: each one is in or above a never path", recs)
	}
}

func TestRecsOutsideRoot(t *testing.T) {
	tree := fakeTree("linux", "/home/dev", []fakeFile{{"x/y", 500 * mb, fresh}})
	tree.Prune(DefaultMinBytes, DefaultMinFrac, DefaultMaxDepth)
	rs := RuleSet{OS: "linux", Rules: []Rule{{ID: "apt", Group: Safe, Match: func(*Match) []Hit {
		return []Hit{{Targets: []Target{{Path: "/var/cache/apt"}}, Bytes: 100 * mb}}
	}}}}
	if recs := rs.Apply(tree, "/home/dev", goldenNow); len(recs) != 0 {
		t.Errorf("recs = %+v, want none for a target outside the root", recs)
	}
}

func TestRecsUniqueIDsAndOrder(t *testing.T) {
	var files []fakeFile
	for _, app := range []string{"a", "b"} {
		for _, d := range []string{"20260101", "20260201", "20260301", "20260401"} {
			files = append(files, fakeFile{"srv/" + app + "/releases/" + d + "/x", 50 * mb, stale})
		}
	}
	files = append(files, fakeFile{"home/dev/.npm/_cacache/x", 20 * mb, fresh})
	tree := fakeTree("linux", "/", files)
	tree.Prune(DefaultMinBytes, DefaultMinFrac, DefaultMaxDepth)
	recs := RulesFor("linux").Apply(tree, "/home/dev", goldenNow)
	var ids []string
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	if got, want := strings.Join(ids, ","), "npm-cache,old-releases,old-releases-2"; got != want {
		t.Errorf("ids = %s, want %s", got, want)
	}
	if recs[1].Bytes != 100*mb || len(recs[1].Cmd.Sh) != 2 {
		t.Errorf("old releases = %+v, want the two oldest", recs[1])
	}
}

func TestStaleNodeModules(t *testing.T) {
	files := []fakeFile{
		{"p/old/package.json", 100, stale},
		{"p/old/node_modules/x/y", 300 * mb, fresh}, // node_modules is newer than its project
		{"p/new/package.json", 100, fresh},
		{"p/new/node_modules/x/y", 300 * mb, stale},
		{"p/noproj/node_modules/x/y", 300 * mb, stale},
		{"p/old/node_modules/dep/node_modules/z", 50 * mb, stale},
		{"p/.hidden/proj/package.json", 100, stale},
		{"p/.hidden/proj/node_modules/q", 300 * mb, stale},
	}
	tree := fakeTree("linux", "/home/dev", files)
	tree.Prune(DefaultMinBytes, DefaultMinFrac, DefaultMaxDepth)
	hits := staleNodeModules(90 * 24 * time.Hour)(&Match{t: tree, home: "/home/dev", now: goldenNow})
	if len(hits) != 1 || len(hits[0].Targets) != 1 || hits[0].Targets[0].Path != "/home/dev/p/old/node_modules" {
		t.Errorf("hits = %+v, want only p/old/node_modules", hits)
	}
}

func TestPrune(t *testing.T) {
	files := []fakeFile{
		{"big/a/b/c/file", 600 * mb, fresh},
		{"big/small1", 1 * mb, fresh},
		{"big/smalldir/x", 2 * mb, fresh},
		{"mid/x", 100 * mb, fresh},
		{"mid/y", 100 * mb, fresh},
		{"tiny/z", 10, fresh},
	}
	tree := fakeTree("linux", "/data", files)
	tree.Prune(DefaultMinBytes, DefaultMinFrac, 3)
	root := tree.Schema()
	var check func(n schema.ScanNode)
	check = func(n schema.ScanNode) {
		if len(n.C) == 0 {
			return
		}
		var sum int64
		for _, c := range n.C {
			sum += c.B
			check(c)
		}
		if sum != n.B {
			t.Errorf("node %s: children sum %d, want %d", n.N, sum, n.B)
		}
	}
	check(root)
	if root.N != "/data" || root.B != 803*mb+10 {
		t.Errorf("root = %s %d", root.N, root.B)
	}
	big := root.C[0]
	if big.N != "big" || big.C[0].N != "a/b" || !big.C[len(big.C)-1].O {
		t.Errorf("big = %+v, want a chain node a/b cut at depth 3 and an other node", big)
	}
	if n := tree.byPath["/data/big/a/b"]; n == nil || n != tree.byPath["/data/big/a"] {
		t.Error("byPath of a chain node: want one node for each path of the chain")
	}
	last := root.C[len(root.C)-1]
	if !last.O || last.B != 10 {
		t.Errorf("last = %+v, want other node with tiny", last)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"50MB": 50_000_000, "1.5GB": 1_500_000_000, "512KiB": 512 << 10,
		"2gib": 2 << 30, "100": 100, "1 TB": 1e12, "10B": 10,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "MB", "-1MB", "ten"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q): got no error", in)
		}
	}
}

func TestRuleTables(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		rs := RulesFor(goos)
		if len(rs.Rules) == 0 || len(rs.Never) == 0 {
			t.Errorf("%s: no rules or never list", goos)
		}
		ids := map[string]bool{}
		for _, r := range rs.Rules {
			if ids[r.ID] {
				t.Errorf("%s: rule id %s is not unique", goos, r.ID)
			}
			ids[r.ID] = true
			if r.Group != Safe && r.Group != Review {
				t.Errorf("%s/%s: bad group %q", goos, r.ID, r.Group)
			}
		}
	}
}
