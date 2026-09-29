package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	cases := []struct {
		in     string
		name   string
		ppid   int32
		zombie bool
	}{
		{"42 (bash) S 1 42 42 0 -1", "bash", 1, false},
		{"43 (oom_tripwire) Z 2661215 1 1 0", "oom_tripwire", 2661215, true},
		{"44 (a (weird) name) R 7 44 44", "a (weird) name", 7, false},
	}
	for _, c := range cases {
		e, ok := parseProcStat([]byte(c.in))
		if !ok {
			t.Fatalf("parse failed: %q", c.in)
		}
		if e.name != c.name || e.ppid != c.ppid || e.zombie != c.zombie {
			t.Errorf("%q → %+v", c.in, e)
		}
	}
	if _, ok := parseProcStat([]byte("garbage")); ok {
		t.Error("garbage should not parse")
	}
}

func TestReadPSI(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cpu", "some avg10=12.50 avg60=3.00 avg300=1.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	write("memory", "some avg10=4.20 avg60=1.00 avg300=0.00 total=1\nfull avg10=1.10 avg60=0.00 avg300=0.00 total=1\n")
	write("io", "some avg10=30.00 avg60=0.00 avg300=0.00 total=1\nfull avg10=15.00 avg60=0.00 avg300=0.00 total=1\n")
	p := readPSI(dir)
	if p == nil {
		t.Fatal("nil PSI")
	}
	if p.CPUSome != 12.5 || p.MemSome != 4.2 || p.MemFull != 1.1 || p.IOSome != 30 || p.IOFull != 15 {
		t.Errorf("got %+v", *p)
	}
	if readPSI(filepath.Join(dir, "missing")) != nil {
		t.Error("missing dir should return nil")
	}
}

func TestScanProcStatFakeRoot(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, stat string) {
		d := filepath.Join(root, pid)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
	}
	mk("1", "1 (init) S 0 1 1")
	mk("5", "5 (dead) Z 1 1 1")
	os.MkdirAll(filepath.Join(root, "self"), 0o755)
	entries, err := scanProcStat(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
}
