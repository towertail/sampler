package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/towertail/sampler/internal/push"
	"github.com/towertail/sampler/pkg/schema"
)

// asSampler makes the test binary act as the sampler. The push test
// starts the test binary as the scan subprocess.
const asSampler = "TOWERTAIL_TEST_AS_SAMPLER"

func TestMain(m *testing.M) {
	if os.Getenv(asSampler) == "1" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// TestPushRunsRealScan runs push mode against a fake server. The
// server queues one disk_scan job, and the real scan subcommand scans
// a temp folder.
func TestPushRunsRealScan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big"), bytes.Repeat([]byte("a"), 20000), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(asSampler, "1")

	var (
		mu     sync.Mutex
		queued = true
		lines  []string
		result *schema.ScanResult
	)
	got := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/control/next":
			if !queued {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			queued = false
			fmt.Fprintf(w, `{"id":"m1","kind":"disk_scan","payload":{"job_id":"j1","root":%q,"max_depth":2,"min_bytes":1000}}`, root)
		case "/v1/disk-scans/j1/progress":
			var l schema.ScanError
			_ = json.NewDecoder(r.Body).Decode(&l)
			lines = append(lines, l.T+" "+l.Message)
			io.WriteString(w, `{"cancel":false}`)
		case "/v1/disk-scans/j1/result":
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var res schema.ScanResult
			if err := json.NewDecoder(gz).Decode(&res); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			result = &res
			w.WriteHeader(http.StatusNoContent)
			close(got)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = push.Run(ctx, push.Config{
			Endpoint: srv.URL, Token: "tok",
			Interval: time.Hour, Heartbeat: 10 * time.Millisecond,
			AllowDiskScan: true,
		}, func() schema.Sample { return schema.Sample{V: schema.SchemaVersion} }, io.Discard)
	}()
	select {
	case <-got:
	case <-time.After(30 * time.Second):
		mu.Lock()
		t.Fatalf("no result; lines = %v", lines)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if result.Files != 1 || result.ScannedBytes < 20000 {
		t.Errorf("result = %+v", result)
	}
	for _, l := range lines {
		if l != "progress " {
			t.Errorf("line %q", l)
		}
	}
}
