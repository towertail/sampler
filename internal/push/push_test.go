package push

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// reply is one answer of the ingest test server.
type reply struct {
	status int
	body   string
}

// ingestServer records each batch and answers with the next reply
// (200 with no body when the list is empty).
type ingestServer struct {
	mu      sync.Mutex
	replies []reply
	auth    []string
	agents  []string
	batches [][]schema.Sample
}

func (s *ingestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := reply{status: http.StatusOK}
	if len(s.replies) > 0 {
		rep, s.replies = s.replies[0], s.replies[1:]
	}
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	s.agents = append(s.agents, r.Header.Get("User-Agent"))
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
	w.WriteHeader(rep.status)
	_, _ = w.Write([]byte(rep.body))
}

func (s *ingestServer) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.batches)
}

// recorder keeps the pushed results.
type recorder struct {
	mu      sync.Mutex
	results []Result
}

func (r *recorder) PushResult(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

func (r *recorder) last() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.results) == 0 {
		return Result{}
	}
	return r.results[len(r.results)-1]
}

func newTestPusherCfg(t *testing.T, cfg Config, replies ...reply) (*pusher, *ingestServer, *bytes.Buffer) {
	t.Helper()
	is := &ingestServer{replies: replies}
	srv := httptest.NewServer(is)
	t.Cleanup(srv.Close)
	stderr := &bytes.Buffer{}
	cfg.Endpoint, cfg.Token, cfg.BatchSize, cfg.Buffer = srv.URL, "tok", 10, 3
	return newPusher(cfg, stderr), is, stderr
}

func newTestPusher(t *testing.T, statuses ...int) (*pusher, *ingestServer, *bytes.Buffer) {
	t.Helper()
	replies := make([]reply, len(statuses))
	for i, st := range statuses {
		replies[i] = reply{status: st}
	}
	return newTestPusherCfg(t, Config{}, replies...)
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

func TestFlush401Requeues(t *testing.T) {
	p, is, stderr := newTestPusher(t, http.StatusUnauthorized, http.StatusForbidden, http.StatusAccepted)
	p.ring.push(sample("a"))
	for i := 0; i < 2; i++ {
		var fe *flushError
		if err := p.attemptFlush(context.Background()); !errors.As(err, &fe) || !fe.auth {
			t.Fatalf("attempt %d: err = %v, want auth error", i, err)
		}
	}
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(is.batches) != 3 || len(is.batches[2]) != 1 || is.batches[2][0].TS != "a" {
		t.Fatalf("batches = %v", is.batches)
	}
	if !strings.Contains(stderr.String(), "push: auth rejected (HTTP 401); check the token") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFlush202IntervalResetsTicker(t *testing.T) {
	p, _, _ := newTestPusherCfg(t, Config{Interval: 30 * time.Second},
		reply{http.StatusAccepted, `{"accepted":1,"interval":"5s"}`},
		reply{http.StatusAccepted, `{}`},
		reply{http.StatusAccepted, `{}`})
	for _, want := range []time.Duration{5 * time.Second, 30 * time.Second} {
		p.ring.push(sample("a"))
		if err := p.attemptFlush(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-p.intervalCh:
			if got != want {
				t.Fatalf("interval = %s, want %s", got, want)
			}
		default:
			t.Fatalf("no interval sent, want %s", want)
		}
	}
	// The interval did not change, so nothing is sent.
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.intervalCh) != 0 {
		t.Fatal("interval sent again with no change")
	}
}

func TestFlush202BadIntervalIgnored(t *testing.T) {
	p, _, stderr := newTestPusherCfg(t, Config{Interval: 30 * time.Second},
		reply{http.StatusAccepted, `{"interval":"x"}`})
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.intervalCh) != 0 || p.interval != 30*time.Second {
		t.Fatalf("interval changed to %s", p.interval)
	}
	if n := strings.Count(stderr.String(), "\n"); n != 1 || !strings.Contains(stderr.String(), `ignore interval "x"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFlush202UpdateRecorded(t *testing.T) {
	rec := &recorder{}
	p, _, _ := newTestPusherCfg(t, Config{Interval: 30 * time.Second, Recorder: rec},
		reply{http.StatusAccepted, `{"accepted":1,"update":"0.7.1"}`})
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := rec.last(); r.Update != "0.7.1" || r.Status != http.StatusAccepted || r.Interval != 30*time.Second {
		t.Fatalf("result = %+v", r)
	}
}

func TestFlush402PausesAndRequeues(t *testing.T) {
	rec := &recorder{}
	p, is, _ := newTestPusherCfg(t, Config{Recorder: rec},
		reply{http.StatusPaymentRequired, `{"retry_after":120}`})
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.last().PausedUntil; !got.Equal(now.Add(120 * time.Second)) {
		t.Fatalf("paused until %v", got)
	}
	now = now.Add(119 * time.Second)
	if err := p.attemptFlush(context.Background()); err != nil || is.requests() != 1 {
		t.Fatalf("request while paused: err=%v requests=%d", err, is.requests())
	}
	now = now.Add(2 * time.Second)
	if err := p.attemptFlush(context.Background()); err != nil || is.requests() != 2 {
		t.Fatalf("no request after the pause: err=%v requests=%d", err, is.requests())
	}
	if len(is.batches[1]) != 1 || is.batches[1][0].TS != "a" {
		t.Fatalf("batches = %v", is.batches)
	}
}

func TestFlush402DefaultAndClamp(t *testing.T) {
	for _, tc := range []struct {
		body string
		want time.Duration
	}{
		{`{}`, time.Hour},
		{`{"retry_after":5}`, time.Minute},
		{`{"retry_after":999999}`, 24 * time.Hour},
	} {
		p, _, _ := newTestPusherCfg(t, Config{}, reply{http.StatusPaymentRequired, tc.body})
		now := time.Unix(1000, 0)
		p.now = func() time.Time { return now }
		p.ring.push(sample("a"))
		if err := p.attemptFlush(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := p.pausedUntil.Sub(now); got != tc.want {
			t.Errorf("body %s: pause %s, want %s", tc.body, got, tc.want)
		}
	}
}

func TestFlush410ReturnsGone(t *testing.T) {
	is := &ingestServer{replies: []reply{{http.StatusGone, `{"action":"exit","reason":"host_removed"}`}}}
	srv := httptest.NewServer(is)
	defer srv.Close()
	rec := &recorder{}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			Endpoint: srv.URL, Token: "tok", Interval: time.Hour,
			FlushInterval: 10 * time.Millisecond, Recorder: rec,
		}, func() schema.Sample { return sample("a") }, io.Discard)
	}()
	select {
	case err := <-done:
		var ge *GoneError
		if !errors.As(err, &ge) || ge.Reason != "host_removed" {
			t.Fatalf("err = %v, want GoneError host_removed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on 410")
	}
	if rec.last().Gone != "host_removed" {
		t.Fatalf("result = %+v", rec.last())
	}
	// No final flush after a 410.
	if n := is.requests(); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestFlush503HonorsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p := newPusher(Config{Endpoint: srv.URL, Token: "tok", BatchSize: 10, Buffer: 3}, io.Discard)
	p.ring.push(sample("a"))
	var fe *flushError
	if err := p.attemptFlush(context.Background()); !errors.As(err, &fe) || fe.retryAfter != 7*time.Second {
		t.Fatalf("err = %v", err)
	}
}

func TestFlushUserAgent(t *testing.T) {
	p, is, _ := newTestPusherCfg(t, Config{UserAgent: "towertail-sampler/0.7.0"})
	p.ring.push(sample("a"))
	if err := p.attemptFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if is.agents[0] != "towertail-sampler/0.7.0" {
		t.Fatalf("user agent = %q", is.agents[0])
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
