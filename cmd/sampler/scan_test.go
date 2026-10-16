package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/towertail/sampler/pkg/schema"
)

// setStdin sets the stdin of the scan command for one test.
func setStdin(t *testing.T, r io.Reader) {
	old := stdin
	stdin = r
	t.Cleanup(func() { stdin = old })
}

// lines decodes the NDJSON lines of out and returns their t values.
func lines(t *testing.T, out []byte) ([]string, []json.RawMessage) {
	t.Helper()
	var types []string
	var raw []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		var l schema.ScanLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		if l.V != schema.ScanVersion {
			t.Errorf("v = %d", l.V)
		}
		types = append(types, l.T)
		raw = append(raw, append(json.RawMessage(nil), sc.Bytes()...))
	}
	return types, raw
}

func TestScanResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	setStdin(t, pr)

	var out, errb bytes.Buffer
	if code := run([]string{"scan", "--root", root, "--min-bytes", "1KB"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	types, raw := lines(t, out.Bytes())
	if len(types) == 0 || types[len(types)-1] != schema.ScanLineResult {
		t.Fatalf("lines = %v, want a result line last", types)
	}
	dec := json.NewDecoder(bytes.NewReader(raw[len(raw)-1]))
	dec.DisallowUnknownFields()
	var res schema.ScanResult
	if err := dec.Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 || res.Recs == nil || res.Tree.N == "" {
		t.Errorf("result = %+v", res)
	}
	if res.Limits == nil || res.Limits.MaxDepth != 6 || res.Limits.MinBytes < 1 {
		t.Errorf("limits = %+v", res.Limits)
	}
}

func TestScanMinFracZero(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	setStdin(t, pr)

	var out, errb bytes.Buffer
	if code := run([]string{"scan", "--root", root, "--min-bytes", "1KB", "--min-frac", "0", "--max-depth", "2"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	_, raw := lines(t, out.Bytes())
	var res schema.ScanResult
	if err := json.Unmarshal(raw[len(raw)-1], &res); err != nil {
		t.Fatal(err)
	}
	if l := res.Limits; l == nil || l.MaxDepth != 2 || l.MinBytes != 1000 {
		t.Errorf("limits = %+v, want depth 2 and 1000 bytes", l)
	}
}

func TestScanBadFlag(t *testing.T) {
	setStdin(t, strings.NewReader(""))
	var out, errb bytes.Buffer
	if code := run([]string{"scan", "--min-bytes", "lots"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if types, _ := lines(t, out.Bytes()); len(types) != 1 || types[0] != schema.ScanLineError {
		t.Errorf("lines = %v, want one error line", types)
	}
}

func TestScanMissingRoot(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	setStdin(t, pr)
	var out, errb bytes.Buffer
	if code := run([]string{"scan", "--root", filepath.Join(t.TempDir(), "missing")}, &out, &errb); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if types, _ := lines(t, out.Bytes()); len(types) != 1 || types[0] != schema.ScanLineError {
		t.Errorf("lines = %v, want one error line", types)
	}
}

// lineWriter calls first when the first line arrives.
type lineWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	once  sync.Once
	first func()
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.once.Do(w.first)
	return w.buf.Write(p)
}

func TestScanCancelOnStdinEOF(t *testing.T) {
	root := t.TempDir()
	for i := range 200 {
		dir := filepath.Join(root, fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := range 100 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(j)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	pr, pw := io.Pipe()
	setStdin(t, pr)
	w := &lineWriter{first: func() { pw.Close() }}
	var errb bytes.Buffer
	if code := run([]string{"scan", "--root", root, "--progress-interval", "1ms"}, w, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	types, _ := lines(t, w.buf.Bytes())
	last := types[len(types)-1]
	if last == schema.ScanLineResult {
		t.Skip("the scan ended before the first progress line")
	}
	if last != schema.ScanLineCancelled {
		t.Errorf("lines = %v, want cancelled last", types)
	}
}
