package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSampleJSONKeysMatchSwift(t *testing.T) {
	disks := []DiskSample{{Mount: "/", Fs: "apfs", Used: 1, Total: 2}}
	s := Sample{
		V:  1,
		TS: FormatTS(time.Date(2026, 4, 20, 19, 42, 7, 103*int(time.Millisecond), time.UTC)),
		Host: HostInfo{
			Name: "h", OS: "linux", Arch: "arm64", Kernel: "6.6",
			UptimeS: 1000, Sampler: "0.1.0+abc",
		},
		CPU:    CPUInfo{Pct: 42.3, Load1: 1.24, Load5: 0.98, Load15: 0.81, Cores: 8},
		Mem:    MemInfo{Used: 10, Total: 20},
		Swap:   MemInfo{Used: 0, Total: 0},
		Disks:  &disks,
		Net:    &NetInfo{RxBps: 1, TxBps: 2, RxCum: 3, TxCum: 4},
		Errors: []string{},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)

	// Keys that must exist exactly (snake_case, mirroring Sample.swift).
	wantKeys := []string{
		`"v":1`,
		`"ts":"2026-04-20T19:42:07.103Z"`,
		`"uptime_s":1000`,
		`"load_1":1.24`,
		`"load_5":0.98`,
		`"load_15":0.81`,
		`"rx_bps":1`,
		`"tx_bps":2`,
		`"rx_cum":3`,
		`"tx_cum":4`,
		`"errors":[]`,
	}
	for _, k := range wantKeys {
		if !strings.Contains(out, k) {
			t.Errorf("json missing %q:\n%s", k, out)
		}
	}

	// Keys that must NOT appear (Swift-side camelCase).
	forbidden := []string{"uptimeS", "load1", "rxBps"}
	for _, k := range forbidden {
		if strings.Contains(out, k) {
			t.Errorf("json contains forbidden key %q:\n%s", k, out)
		}
	}
}

func TestMachineIDOmitsWhenEmpty(t *testing.T) {
	s := Sample{V: 1, TS: FormatTS(time.Now()), Errors: []string{}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"machine_id"`) {
		t.Errorf("machine_id should be omitted when empty: %s", b)
	}
}

func TestMachineIDEmittedWhenPresent(t *testing.T) {
	s := Sample{
		V:      1,
		TS:     FormatTS(time.Now()),
		Host:   HostInfo{MachineID: "abc-123"},
		Errors: []string{},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"machine_id":"abc-123"`) {
		t.Errorf("expected machine_id in output: %s", b)
	}
}

func TestDisksAndNetOmitWhenNil(t *testing.T) {
	s := Sample{V: 1, TS: FormatTS(time.Now()), Errors: []string{}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, `"disks"`) {
		t.Errorf("disks should be omitted when nil: %s", out)
	}
	if strings.Contains(out, `"net"`) {
		t.Errorf("net should be omitted when nil: %s", out)
	}
}

func TestErrorsNeverNull(t *testing.T) {
	s := Sample{V: 1, TS: FormatTS(time.Now()), Errors: []string{}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"errors":[]`) {
		t.Errorf("expected empty errors array, got %s", b)
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
