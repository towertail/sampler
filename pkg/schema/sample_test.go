package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSampleJSONOmit(t *testing.T) {
	zero := 0
	base := Sample{V: 1, TS: FormatTS(time.Now()), Errors: []string{}}
	withHost := base
	withHost.Host = HostInfo{MachineID: "abc-123"}
	tests := []struct {
		name   string
		v      any
		has    []string
		hasNot []string
	}{
		{"machine_id empty", base, nil, []string{`"machine_id"`}},
		{"machine_id set", withHost, []string{`"machine_id":"abc-123"`}, nil},
		{"disks and net nil", base, nil, []string{`"disks"`, `"net"`}},
		{"errors empty", base, []string{`"errors":[]`}, nil},
		{"mem_pressure_pct nil", HealthInfo{}, nil, []string{"mem_pressure_pct"}},
		{"mem_pressure_pct zero", HealthInfo{MemPressurePct: &zero}, []string{`"mem_pressure_pct":0`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range tt.has {
				if !strings.Contains(string(b), k) {
					t.Errorf("missing %s: %s", k, b)
				}
			}
			for _, k := range tt.hasNot {
				if strings.Contains(string(b), k) {
					t.Errorf("unexpected %s: %s", k, b)
				}
			}
		})
	}
}

func TestFormatTSIsUTCWithMillis(t *testing.T) {
	// Ensure the format string produces RFC3339 with ms and Z suffix.
	tm := time.Date(2026, 4, 20, 19, 42, 7, 103000000, time.UTC)
	got := FormatTS(tm)
	want := "2026-04-20T19:42:07.103Z"
	if got != want {
		t.Errorf("FormatTS = %q, want %q", got, want)
	}

	// Non-UTC inputs get normalized.
	loc, _ := time.LoadLocation("America/New_York")
	tm2 := time.Date(2026, 4, 20, 15, 42, 7, 103000000, loc)
	got = FormatTS(tm2)
	if got != "2026-04-20T19:42:07.103Z" {
		t.Errorf("FormatTS non-UTC = %q", got)
	}
}

func TestProcSampleFolderKeysOmitWhenEmpty(t *testing.T) {
	b, err := json.Marshal(ProcSample{PID: 1, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cwd", "repo", "parent", "read_bps", "write_bps"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("empty sample has key %q: %s", k, b)
		}
	}
	zero := int64(0)
	b, err = json.Marshal(ProcSample{PID: 1, Cwd: "/a", Repo: "/a", Parent: "p", ReadBps: &zero, WriteBps: &zero})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"cwd":"/a"`, `"repo":"/a"`, `"parent":"p"`, `"read_bps":0`, `"write_bps":0`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("sample misses %s: %s", k, b)
		}
	}
}
