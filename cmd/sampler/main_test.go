package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/towertail/sampler/internal/collect"
	"github.com/towertail/sampler/internal/version"
	"github.com/towertail/sampler/internal/wiretest"
)

func TestRunVersion(t *testing.T) {
	oldV, oldSHA := version.Version, version.SHA
	t.Cleanup(func() { version.Version, version.SHA = oldV, oldSHA })
	version.Version, version.SHA = "1.2.3", "abc1234"

	const want = "towertail-sampler 1.2.3+abc1234\n"
	for _, arg := range []string{"--version", "-V", "version"} {
		t.Run(arg, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{arg}, &out, &errb); code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, errb.String())
			}
			if out.String() != want {
				t.Errorf("got %q, want %q", out.String(), want)
			}
		})
	}
}

func TestRunSelfCheck(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--self-check"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Errorf("expected 'ok', got %q", out.String())
	}
}

func TestRunOnceEmitsValidSample(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--once"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if v, ok := m["v"].(float64); !ok || int(v) != 1 {
		t.Errorf("v != 1: %v", m["v"])
	}
	for _, k := range []string{"ts", "host", "cpu", "mem", "swap", "errors"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
}

func TestRunOnceNoDiskNoNetOmitsFields(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--once", "--no-disk", "--no-net"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if _, ok := m["disks"]; ok {
		t.Error("disks should be omitted with --no-disk")
	}
	if _, ok := m["net"]; ok {
		t.Error("net should be omitted with --no-net")
	}
}

func TestRunOnceEmitsProcs(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--once", "--top-n", "5"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	procs, ok := m["procs"].(map[string]any)
	if !ok {
		t.Fatalf("expected procs object, got: %v", m["procs"])
	}
	if procs["top_n"].(float64) != 5 {
		t.Errorf("top_n: got %v want 5", procs["top_n"])
	}
	items, ok := procs["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty items array, got: %v", procs["items"])
	}
	// Union of top 5 by CPU and top 5 by RSS ≤ 10.
	if len(items) > 10 {
		t.Errorf("expected ≤ 10 items (union of 2×5), got %d", len(items))
	}
}

func TestRunOnceNoProcOmitsField(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--once", "--no-proc"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if _, ok := m["procs"]; ok {
		t.Error("procs should be omitted with --no-proc")
	}
}

func TestRunDefaultsToOnce(t *testing.T) {
	// Spec §3.1: one-shot is the default for v1.
	var out, errb bytes.Buffer
	code := run([]string{}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
}

func TestLiveSampleMatchesFixtureKeys(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/sample-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	want := wiretest.Keys(fixture)

	s := buildSample(&options{noPorts: true, topN: 5, procScanMax: collect.DefaultProcScanMax}, collect.SampleWindow, nil)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var live any
	if err := json.Unmarshal(b, &live); err != nil {
		t.Fatal(err)
	}
	for k := range wiretest.Keys(live) {
		if !want[k] {
			t.Errorf("live sample key %q is not in testdata/sample-v1.json; update the fixture", k)
		}
	}
}
