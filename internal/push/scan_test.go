package push

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// scanLine is one line that the server got for a job.
type scanLine struct {
	Job     string
	T       string
	Message string
}

// scanServer is the control queue, the ingest endpoint and the disk
// scan endpoints.
type scanServer struct {
	mu        sync.Mutex
	queue     []string
	lines     []scanLine
	cancel    map[string]bool
	putStatus []int
	puts      int
	results   map[string]schema.ScanResult
	batches   int
	errs      []string
}

func newScanServer(t *testing.T, queue ...string) (*scanServer, *httptest.Server) {
	t.Helper()
	s := &scanServer{queue: queue, cancel: map[string]bool{}, results: map[string]schema.ScanResult{}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *scanServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		s.errs = append(s.errs, "auth "+r.URL.Path)
	}
	switch {
	case r.URL.Path == "/v1/control/next":
		if len(s.queue) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		io.WriteString(w, s.queue[0])
		s.queue = s.queue[1:]
	case r.URL.Path == "/v1/ingest/samples":
		s.batches++
	case strings.HasSuffix(r.URL.Path, "/progress") && r.Method == http.MethodPost:
		job := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/disk-scans/"), "/progress")
		var l struct {
			T       string `json:"t"`
			V       int    `json:"v"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&l); err != nil || l.V != schema.ScanVersion {
			s.errs = append(s.errs, fmt.Sprintf("bad line: %v", err))
		}
		s.lines = append(s.lines, scanLine{Job: job, T: l.T, Message: l.Message})
		fmt.Fprintf(w, `{"cancel":%t}`, s.cancel[job])
	case strings.HasSuffix(r.URL.Path, "/result") && r.Method == http.MethodPut:
		job := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/disk-scans/"), "/result")
		s.puts++
		if len(s.putStatus) > 0 {
			st := s.putStatus[0]
			s.putStatus = s.putStatus[1:]
			if st >= 300 {
				w.WriteHeader(st)
				return
			}
		}
		if r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Content-Type") != "application/json" {
			s.errs = append(s.errs, "result headers")
		}
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			s.errs = append(s.errs, err.Error())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var res schema.ScanResult
		if err := json.NewDecoder(gz).Decode(&res); err != nil || res.T != schema.ScanLineResult {
			s.errs = append(s.errs, fmt.Sprintf("bad result: %v", err))
		}
		s.results[job] = res
		w.WriteHeader(http.StatusNoContent)
	default:
		s.errs = append(s.errs, "unknown "+r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *scanServer) setCancel(job string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel[job] = true
}

func (s *scanServer) jobLines(job string) []scanLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []scanLine
	for _, l := range s.lines {
		if l.Job == job {
			out = append(out, l)
		}
	}
	return out
}

// put gives the number of result PUTs and the result of job.
func (s *scanServer) put(job string) (int, schema.ScanResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.results[job]
	return s.puts, res, ok
}

func (s *scanServer) check(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		t.Errorf("server errors: %v", s.errs)
	}
}

func diskScanMsg(job string) string {
	return fmt.Sprintf(`{"id":"m-%s","kind":"disk_scan","payload":{"job_id":%q,"root":"/x","max_depth":3,"min_bytes":1000}}`, job, job)
}

// fakeScan writes progress lines each `every`. With progress >= 0 it
// writes a result line after that many progress lines. Stop makes it
// write a cancelled line.
type fakeScan struct {
	progress int
	every    time.Duration

	mu   sync.Mutex
	args [][]string
}

func (f *fakeScan) start(_ context.Context, args []string) (*ScanProcess, error) {
	f.mu.Lock()
	f.args = append(f.args, args)
	f.mu.Unlock()
	pr, pw := io.Pipe()
	stopped := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		defer pw.Close()
		enc := json.NewEncoder(pw)
		for i := 0; f.progress < 0 || i < f.progress; i++ {
			select {
			case <-stopped:
				_ = enc.Encode(schema.ScanCancelled{T: schema.ScanLineCancelled, V: schema.ScanVersion})
				return
			case <-time.After(f.every):
			}
			_ = enc.Encode(schema.ScanProgress{T: schema.ScanLineProgress, V: schema.ScanVersion, Files: int64(i)})
		}
		_ = enc.Encode(schema.ScanResult{T: schema.ScanLineResult, V: schema.ScanVersion, Root: "/x", Recs: []schema.ScanRec{}})
	}()
	return &ScanProcess{
		Stdout: pr,
		Stop:   func() { once.Do(func() { close(stopped) }) },
		Wait:   func() error { <-done; return nil },
	}, nil
}

func (f *fakeScan) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.args)
}

// syncWriter is a stderr that more goroutines can write.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func newScanPusher(t *testing.T, url string, allow bool, f *fakeScan) (*pusher, *syncWriter) {
	t.Helper()
	stderr := &syncWriter{}
	p := newPusher(Config{
		Endpoint: url, Token: "tok", Buffer: 100, BatchSize: 10,
		Interval: 10 * time.Millisecond, FlushInterval: 10 * time.Millisecond, Heartbeat: 10 * time.Millisecond,
		AllowControl: true, AllowDiskScan: allow,
	}, stderr)
	p.scans.start = f.start
	p.scans.backoff = time.Millisecond
	return p, stderr
}

func lineTypes(ls []scanLine) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.T
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScanJobFull(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	f := &fakeScan{progress: 2, every: time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, true, f)
	p.pollControl(context.Background())
	p.scans.Wait()

	if got := lineTypes(s.jobLines("j1")); strings.Join(got, ",") != "progress,progress" {
		t.Errorf("lines = %v", got)
	}
	if _, res, ok := s.put("j1"); !ok || res.Root != "/x" {
		t.Errorf("result = %+v, %v", res, ok)
	}
	want := "scan --progress-interval 2s --root /x --max-depth 3 --min-bytes 1000"
	if got := strings.Join(f.args[0], " "); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	s.check(t)
}

func TestScanJobMinFrac(t *testing.T) {
	msg := `{"id":"m-j1","kind":"disk_scan","payload":{"job_id":"j1","root":"/x/y","max_depth":2,"min_bytes":7000,"min_frac":0}}`
	s, srv := newScanServer(t, msg)
	f := &fakeScan{progress: 0, every: time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, true, f)
	p.pollControl(context.Background())
	p.scans.Wait()

	want := "scan --progress-interval 2s --root /x/y --max-depth 2 --min-bytes 7000 --min-frac 0"
	if got := strings.Join(f.args[0], " "); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	s.check(t)
}

func TestScanCancel(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	f := &fakeScan{progress: -1, every: time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, true, f)
	p.pollControl(context.Background())
	waitFor(t, "progress", func() bool { return len(s.jobLines("j1")) >= 2 })
	s.setCancel("j1")
	p.scans.Wait()

	ls := s.jobLines("j1")
	if last := ls[len(ls)-1]; last.T != schema.ScanLineCancelled {
		t.Errorf("lines = %v, want cancelled last", lineTypes(ls))
	}
	if puts, _, _ := s.put("j1"); puts != 0 {
		t.Errorf("puts = %d", puts)
	}
	s.check(t)
}

func TestScanRefusedWhenOff(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	f := &fakeScan{progress: 1, every: time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, false, f)
	p.pollControl(context.Background())
	p.scans.Wait()

	ls := s.jobLines("j1")
	if len(ls) != 1 || ls[0].T != schema.ScanLineError || ls[0].Message != msgDiskScanOff {
		t.Errorf("lines = %+v", ls)
	}
	if f.starts() != 0 {
		t.Errorf("scan started %d times", f.starts())
	}
	s.check(t)
}

func TestScanResultRetry(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	s.putStatus = []int{http.StatusInternalServerError, http.StatusBadGateway}
	f := &fakeScan{progress: 0}
	p, stderr := newScanPusher(t, srv.URL, true, f)
	p.pollControl(context.Background())
	p.scans.Wait()

	if puts, _, ok := s.put("j1"); puts != 3 || !ok {
		t.Errorf("puts = %d, result = %v, want 3 and a result", puts, ok)
	}
	if ls := s.jobLines("j1"); len(ls) != 0 {
		t.Errorf("lines = %+v", ls)
	}
	if n := strings.Count(stderr.String(), "result attempt"); n != 2 {
		t.Errorf("stderr = %q", stderr.String())
	}
	s.check(t)
}

func TestScanResultFailsAfterRetries(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	s.putStatus = []int{500, 500, 500, 500}
	p, _ := newScanPusher(t, srv.URL, true, &fakeScan{progress: 0})
	p.pollControl(context.Background())
	p.scans.Wait()

	if puts, _, _ := s.put("j1"); puts != 1+resultRetries {
		t.Errorf("puts = %d", puts)
	}
	ls := s.jobLines("j1")
	if len(ls) != 1 || ls[0].T != schema.ScanLineError || !strings.HasPrefix(ls[0].Message, "upload the result:") {
		t.Errorf("lines = %+v", ls)
	}
}

func TestScanSecondJobRefused(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"), diskScanMsg("j2"))
	f := &fakeScan{progress: -1, every: time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, true, f)
	p.pollControl(context.Background())
	waitFor(t, "progress", func() bool { return len(s.jobLines("j1")) >= 1 })
	p.pollControl(context.Background())

	ls := s.jobLines("j2")
	if len(ls) != 1 || ls[0].T != schema.ScanLineError || ls[0].Message != msgScanBusy {
		t.Errorf("j2 lines = %+v", ls)
	}
	s.setCancel("j1")
	p.scans.Wait()
	if f.starts() != 1 {
		t.Errorf("starts = %d", f.starts())
	}
	s.check(t)
}

func TestScanStopsWithoutFinalLine(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	p, _ := newScanPusher(t, srv.URL, true, &fakeScan{})
	p.scans.start = func(context.Context, []string) (*ScanProcess, error) {
		return &ScanProcess{
			Stdout: strings.NewReader(""),
			Stop:   func() {},
			Wait:   func() error { return fmt.Errorf("signal: killed") },
		}, nil
	}
	p.pollControl(context.Background())
	p.scans.Wait()
	ls := s.jobLines("j1")
	if len(ls) != 1 || ls[0].Message != "the scan stopped with no result: signal: killed" {
		t.Errorf("lines = %+v", ls)
	}
}

func TestSamplesContinueDuringScan(t *testing.T) {
	s, srv := newScanServer(t, diskScanMsg("j1"))
	f := &fakeScan{progress: -1, every: 5 * time.Millisecond}
	p, _ := newScanPusher(t, srv.URL, true, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx, func() schema.Sample { return sample("x") })
	}()

	waitFor(t, "scan start", func() bool { return len(s.jobLines("j1")) >= 1 })
	s.mu.Lock()
	before := s.batches
	s.mu.Unlock()
	waitFor(t, "batches during the scan", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.batches >= before+3
	})
	if ls := s.jobLines("j1"); ls[len(ls)-1].T != schema.ScanLineProgress {
		t.Errorf("the scan ended: %v", lineTypes(ls))
	}
	s.setCancel("j1")
	waitFor(t, "cancelled", func() bool {
		ls := s.jobLines("j1")
		return ls[len(ls)-1].T == schema.ScanLineCancelled
	})
	cancel()
	<-done
	s.check(t)
}
