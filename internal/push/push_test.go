package push

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// ingestServer records each batch and answers with the next status in
// statuses (200 when the list is empty).
type ingestServer struct {
	mu       sync.Mutex
	statuses []int
	auth     []string
	batches  [][]schema.Sample
}

func (s *ingestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := http.StatusOK
	if len(s.statuses) > 0 {
		status, s.statuses = s.statuses[0], s.statuses[1:]
	}
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	var batch []schema.Sample
	if r.Header.Get("Content-Encoding") == "gzip" && r.Header.Get("Content-Type") == "application/x-ndjson" {
		gz, err := gzip.NewReader(r.Body)
		if err == nil {
			sc := bufio.NewScanner(gz)
			for sc.Scan() {
				var smp schema.Sample
				if json.Unmarshal(sc.Bytes(), &smp) == nil {
					batch = append(batch, smp)
				}
			}
		}
	}
	s.batches = append(s.batches, batch)
	w.WriteHeader(status)
}

func newTestPusher(t *testing.T, statuses ...int) (*pusher, *ingestServer, *bytes.Buffer) {
	t.Helper()
	is := &ingestServer{statuses: statuses}
	srv := httptest.NewServer(is)
	t.Cleanup(srv.Close)
	stderr := &bytes.Buffer{}
	p := newPusher(Config{Endpoint: srv.URL, Token: "tok", BatchSize: 10, Buffer: 3}, stderr)
	return p, is, stderr
}

func sample(ts string) schema.Sample { return schema.Sample{V: schema.SchemaVersion, TS: ts} }

func TestFlushSendsGzipNDJSONWithBearer(t *testing.T) {
	p, is, _ := newTestPusher(t)
	p.ring.push(sample("a"))
	p.ring.push(sample("b"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(is.batches) != 1 || len(is.batches[0]) != 2 {
		t.Fatalf("batches = %v", is.batches)
	}
	if is.batches[0][0].TS != "a" || is.batches[0][1].TS != "b" {
		t.Fatalf("order = %v", is.batches[0])
	}
	if is.auth[0] != "Bearer tok" {
		t.Fatalf("auth = %q", is.auth[0])
	}
}

func TestFlushRetriesAfter5xx(t *testing.T) {
	p, is, _ := newTestPusher(t, http.StatusInternalServerError)
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err == nil {
		t.Fatal("want error on 500")
	}
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(is.batches) != 2 || len(is.batches[1]) != 1 || is.batches[1][0].TS != "a" {
		t.Fatalf("batches = %v", is.batches)
	}
}

func TestFlushDropsBatchOnAuthReject(t *testing.T) {
	p, is, stderr := newTestPusher(t, http.StatusUnauthorized, http.StatusForbidden)
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(is.batches) != 1 {
		t.Fatalf("want no retry, got %d requests", len(is.batches))
	}
	if !strings.Contains(stderr.String(), "push: auth rejected (HTTP 401); check the token") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAuthLogIsRateLimited(t *testing.T) {
	p, _, stderr := newTestPusher(t)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	p.logAuthRejected(401)
	now = now.Add(30 * time.Second)
	p.logAuthRejected(401)
	if n := strings.Count(stderr.String(), "auth rejected"); n != 1 {
		t.Fatalf("lines after 30s = %d", n)
	}
	now = now.Add(31 * time.Second)
	p.logAuthRejected(403)
	if n := strings.Count(stderr.String(), "auth rejected"); n != 2 {
		t.Fatalf("lines after 61s = %d", n)
	}
}

func TestRingOverflowDropsOldest(t *testing.T) {
	r := newRing(3)
	for _, ts := range []string{"a", "b", "c", "d"} {
		r.push(sample(ts))
	}
	got := r.drain(10)
	if len(got) != 3 || got[0].TS != "b" || got[2].TS != "d" {
		t.Fatalf("ring = %v", got)
	}
}

func TestKillProcessRefusesInitAndSelf(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, os.Getpid()} {
		if err := killProcess(pid); err == nil {
			t.Fatalf("pid %d not refused", pid)
		}
	}
}

// pollKill sends one kill_process control message for pid.
func pollKill(t *testing.T, pid int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"1","kind":"kill_process","payload":{"pid":%d}}`, pid)
	}))
	defer srv.Close()
	stderr := &bytes.Buffer{}
	p := newPusher(Config{Endpoint: srv.URL, Token: "tok", Buffer: 1, AllowControl: true}, stderr)
	p.pollControl(context.Background())
	return stderr.String()
}

func TestControlKillProcessRefusesInit(t *testing.T) {
	if out := pollKill(t, 1); !strings.Contains(out, "refuse pid 1") {
		t.Fatalf("stderr = %q", out)
	}
}

func TestControlKillProcessKillsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sleep binary")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if out := pollKill(t, cmd.Process.Pid); out != "" {
		t.Fatalf("stderr = %q", out)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child still runs")
	}
}
