// Package push ships collected samples to a Towertail server over
// HTTPS. It buffers samples in-memory, flushes them as NDJSON (gzip
// encoded), and retries with backoff on network / 5xx errors. A
// concurrent heartbeat goroutine long-polls the control endpoint when
// AllowControl or AllowDiskScan is true.
package push

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

type Config struct {
	Endpoint      string
	Token         string
	Interval      time.Duration
	FlushInterval time.Duration
	BatchSize     int
	Buffer        int
	AllowControl  bool
	// AllowDiskScan lets disk_scan control messages start a scan.
	AllowDiskScan bool
	// ScanExe is the sampler binary for scans. Empty is this executable.
	ScanExe   string
	Heartbeat time.Duration
	Insecure  bool
	// Recorder gets the result of each push. Nil means no record.
	Recorder Recorder
	// UserAgent is sent with each ingest request. Empty sends the Go default.
	UserAgent string
	// OnProcCwd gets the proc_cwd key of an ingest reply: true sends the
	// working folders. Nil ignores the key. The flush goroutine calls it.
	OnProcCwd func(send bool)
}

// Recorder takes the result of each push attempt.
type Recorder interface {
	PushResult(r Result)
}

// Result is the outcome of one push attempt and the orders in its reply.
type Result struct {
	At          time.Time
	Status      int
	Err         string
	Interval    time.Duration
	Update      string
	PausedUntil time.Time
	// Gone is the reason of a 410 reply.
	Gone string
}

// GoneError tells that the server removed this sampler (HTTP 410).
type GoneError struct {
	Reason string
}

func (e *GoneError) Error() string { return "server asked the sampler to exit: " + e.Reason }

// SampleFn produces a fresh Sample. Injected so the sampler can reuse
// its own buildSample without push importing collect.
type SampleFn func() schema.Sample

// Run collects until ctx is cancelled. It returns a *GoneError after a
// 410 reply, and nil when the context is done.
func Run(ctx context.Context, cfg Config, sampleFn SampleFn, stderr io.Writer) error {
	if cfg.Endpoint == "" {
		return errors.New("push: endpoint required")
	}
	if cfg.Token == "" {
		return errors.New("push: token required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.Buffer <= 0 {
		cfg.Buffer = 1000
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 15 * time.Second
	}

	return newPusher(cfg, stderr).run(ctx, sampleFn)
}

// run starts the goroutines and waits for them and for a running scan.
// A 410 reply stops all of them and run returns the *GoneError.
func (p *pusher) run(ctx context.Context, sampleFn SampleFn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.collect(ctx, sampleFn) }()

	wg.Add(1)
	go func() { defer wg.Done(); p.flush(ctx, cancel) }()

	if p.cfg.AllowControl || p.cfg.AllowDiskScan {
		wg.Add(1)
		go func() { defer wg.Done(); p.heartbeat(ctx) }()
	}

	wg.Wait()
	p.scans.Wait()
	if p.gone != nil {
		return p.gone
	}
	return nil
}

// authLogEvery limits the auth-rejected log line so a bad token does
// not fill the journal.
const authLogEvery = time.Minute

const (
	maxBackoff     = 30 * time.Second
	maxAuthBackoff = 5 * time.Minute
	// maxRetryAfter limits the Retry-After header of a 5xx reply.
	maxRetryAfter = 5 * time.Minute

	defaultPause = time.Hour
	minPause     = time.Minute
	maxPause     = 24 * time.Hour
	// minOrderInterval is the smallest interval that the sampler accepts from the server.
	minOrderInterval = time.Second
	maxReplyBytes    = 4 << 10
)

type pusher struct {
	cfg    Config
	client *http.Client
	ring   *ring
	scans  *ScanRunner
	stderr io.Writer

	now         func() time.Time
	lastAuthLog time.Time

	// intervalCh carries the push interval from flush to collect.
	intervalCh chan time.Duration
	// The fields below belong to the flush goroutine.
	interval    time.Duration
	pausedUntil time.Time
	gone        *GoneError
}

func newPusher(cfg Config, stderr io.Writer) *pusher {
	client := httpClient(cfg.Insecure)
	return &pusher{
		cfg:    cfg,
		client: client,
		ring:   newRing(cfg.Buffer),
		scans:  NewScanRunner(cfg, client, ExecScanStarter(cfg.ScanExe, stderr), stderr),
		stderr: stderr,
		now:    time.Now,

		intervalCh: make(chan time.Duration, 1),
		interval:   cfg.Interval,
	}
}

// setInterval sends d to the collector. The newest value wins.
func (p *pusher) setInterval(d time.Duration) {
	for {
		select {
		case p.intervalCh <- d:
			return
		default:
		}
		select {
		case <-p.intervalCh:
		default:
		}
	}
}

func (p *pusher) record(r Result) {
	if p.cfg.Recorder == nil {
		return
	}
	if r.At.IsZero() {
		r.At = p.now()
	}
	p.cfg.Recorder.PushResult(r)
}

// collect ticks at cfg.Interval and pushes one sample onto the ring.
func (p *pusher) collect(ctx context.Context, sampleFn SampleFn) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	cur := p.cfg.Interval
	// Emit the first sample immediately so downstream sees data < 1 interval.
	p.ring.push(sampleFn())
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-p.intervalCh:
			if d != cur {
				cur = d
				t.Reset(d)
			}
		case <-t.C:
			p.ring.push(sampleFn())
		}
	}
}

// flush attempts to POST every cfg.FlushInterval or whenever
// cfg.BatchSize is reached. On a 410 reply it stores the order and
// calls cancel.
func (p *pusher) flush(ctx context.Context, cancel context.CancelFunc) {
	t := time.NewTicker(p.cfg.FlushInterval)
	defer t.Stop()
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			if p.gone == nil {
				// Best-effort final flush.
				_ = p.attemptFlush(context.Background())
			}
			return
		case <-t.C:
			err := p.attemptFlush(ctx)
			if err == nil {
				backoff = time.Second
				continue
			}
			var gone *GoneError
			if errors.As(err, &gone) {
				p.gone = gone
				cancel()
				return
			}
			limit, wait := maxBackoff, backoff
			var fe *flushError
			if errors.As(err, &fe) {
				if fe.auth {
					limit = maxAuthBackoff
				}
				wait = max(wait, fe.retryAfter)
				if !fe.auth {
					fmt.Fprintf(p.stderr, "push: flush error: %v\n", err)
				}
			} else {
				fmt.Fprintf(p.stderr, "push: flush error: %v\n", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			backoff = min(backoff*2, limit)
		}
	}
}

// flushError is a rejected push that the flush loop retries.
type flushError struct {
	status int
	// auth is true for 401 and 403. They back off up to maxAuthBackoff.
	auth       bool
	retryAfter time.Duration
}

func (e *flushError) Error() string {
	if e.auth {
		return fmt.Sprintf("auth rejected (HTTP %d)", e.status)
	}
	return fmt.Sprintf("server %d", e.status)
}

func (p *pusher) attemptFlush(ctx context.Context) error {
	if p.now().Before(p.pausedUntil) {
		return nil
	}
	batch := p.ring.drain(p.cfg.BatchSize)
	if len(batch) == 0 {
		return nil
	}
	body, err := encodeBatch(batch)
	if err != nil {
		// Encoding should never fail; drop the batch to avoid a tight
		// loop re-trying a poison pill.
		fmt.Fprintf(p.stderr, "push: encode failed: %v\n", err)
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(p.cfg.Endpoint, "/v1/ingest/samples"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Content-Encoding", "gzip")
	if p.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", p.cfg.UserAgent)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		p.ring.requeue(batch)
		p.record(Result{Err: err.Error()})
		return err
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		p.handleOrders(resp)
		return nil
	case status == http.StatusPaymentRequired:
		p.ring.requeue(batch)
		p.handlePlanFull(resp)
		return nil
	case status == http.StatusGone:
		var order schema.IngestGone
		_ = json.NewDecoder(io.LimitReader(resp.Body, maxReplyBytes)).Decode(&order)
		if order.Reason == "" {
			order.Reason = "unknown"
		}
		p.record(Result{Status: status, Gone: order.Reason})
		return &GoneError{Reason: order.Reason}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// The token can come back, so the batch stays.
		p.ring.requeue(batch)
		p.logAuthRejected(status)
		fe := &flushError{status: status, auth: true}
		p.record(Result{Status: status, Err: fe.Error()})
		return fe
	case status >= 500:
		p.ring.requeue(batch)
		fe := &flushError{status: status, retryAfter: min(retryAfter(resp), maxRetryAfter)}
		p.record(Result{Status: status, Err: fe.Error()})
		return fe
	}
	// Schema-rejection etc. — drop to avoid poison loop.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(b))
	fmt.Fprintf(p.stderr, "push: server %d: %s\n", status, msg)
	p.record(Result{Status: status, Err: fmt.Sprintf("server %d: %s", status, msg)})
	return nil
}

// handleOrders reads the orders of a 2xx reply. No interval key sets
// the interval back to cfg.Interval.
func (p *pusher) handleOrders(resp *http.Response) {
	r := Result{Status: resp.StatusCode, Interval: p.interval}
	var reply schema.IngestResponse
	err := json.NewDecoder(io.LimitReader(resp.Body, maxReplyBytes)).Decode(&reply)
	if err != nil && err != io.EOF {
		fmt.Fprintf(p.stderr, "push: bad reply body: %v\n", err)
		p.record(r)
		return
	}
	if reply.ProcCwd != nil && p.cfg.OnProcCwd != nil {
		p.cfg.OnProcCwd(*reply.ProcCwd)
	}
	d := p.cfg.Interval
	if reply.Interval != "" {
		parsed, perr := time.ParseDuration(reply.Interval)
		if perr != nil || parsed < minOrderInterval {
			fmt.Fprintf(p.stderr, "push: ignore interval %q\n", reply.Interval)
			d = p.interval
		} else {
			d = parsed
		}
	}
	if d != p.interval {
		p.interval = d
		p.setInterval(d)
	}
	r.Interval = d
	r.Update = reply.Update
	p.record(r)
}

// handlePlanFull pauses the pushes for the retry_after of a 402 reply.
func (p *pusher) handlePlanFull(resp *http.Response) {
	var order schema.IngestPlanFull
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxReplyBytes)).Decode(&order)
	d := time.Duration(order.RetryAfter) * time.Second
	if d <= 0 {
		d = retryAfter(resp)
	}
	if d <= 0 {
		d = defaultPause
	}
	d = min(max(d, minPause), maxPause)
	p.pausedUntil = p.now().Add(d)
	fmt.Fprintf(p.stderr, "push: plan is full (HTTP 402); pause for %s\n", d)
	p.record(Result{Status: resp.StatusCode, Err: "plan full", PausedUntil: p.pausedUntil})
}

// retryAfter reads the Retry-After header in seconds. It gives 0 when
// the header is missing or not a number.
func retryAfter(resp *http.Response) time.Duration {
	n, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// logAuthRejected writes the auth line at most once per authLogEvery.
// Only the flush goroutine calls it.
func (p *pusher) logAuthRejected(status int) {
	now := p.now()
	if !p.lastAuthLog.IsZero() && now.Sub(p.lastAuthLog) < authLogEvery {
		return
	}
	p.lastAuthLog = now
	fmt.Fprintf(p.stderr, "push: auth rejected (HTTP %d); check the token\n", status)
}

// heartbeat long-polls the control endpoint at cfg.Heartbeat cadence.
// It kills a process on kill_process when AllowControl is true, and
// gives disk_scan to the ScanRunner.
func (p *pusher) heartbeat(ctx context.Context) {
	t := time.NewTicker(p.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollControl(ctx)
		}
	}
}

func (p *pusher) pollControl(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(p.cfg.Endpoint, "/v1/control/next"), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return
	}
	if resp.StatusCode >= 300 {
		return
	}
	var msg schema.ControlNextResponse
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return
	}
	switch msg.Kind {
	case schema.ControlKindDiskScan:
		if err := p.scans.Handle(ctx, msg); err != nil {
			fmt.Fprintf(p.stderr, "push: disk_scan: %v\n", err)
		}
	case schema.ControlKindKillProcess:
		if !p.cfg.AllowControl {
			fmt.Fprintln(p.stderr, "push: kill_process: refused, --allow-control is off")
			return
		}
		var payload schema.KillProcessPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			return
		}
		if err := killProcess(int(payload.PID)); err != nil {
			fmt.Fprintf(p.stderr, "push: kill_process: %v\n", err)
		}
	}
}

// killProcess refuses init and the sampler itself.
func killProcess(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refuse pid %d", pid)
	}
	if pid == os.Getpid() {
		return fmt.Errorf("refuse own pid %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func encodeBatch(samples []schema.Sample) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	for _, s := range samples {
		if err := enc.Encode(s); err != nil {
			return nil, err
		}
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func joinURL(base, path string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

func httpClient(insecure bool) *http.Client {
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure},
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}
}

// ring is a bounded FIFO of samples. Overflow drops the oldest entry
// so a disconnected sampler doesn't OOM.
type ring struct {
	mu   sync.Mutex
	data []schema.Sample
	max  int
}

func newRing(size int) *ring {
	return &ring{data: make([]schema.Sample, 0, size), max: size}
}

func (r *ring) push(s schema.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.data) == r.max {
		r.data = r.data[1:]
	}
	r.data = append(r.data, s)
}

func (r *ring) drain(n int) []schema.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.data) == 0 {
		return nil
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	out := make([]schema.Sample, n)
	copy(out, r.data[:n])
	r.data = r.data[n:]
	return out
}

// requeue pushes samples back onto the front of the ring. Used after
// transient failures so we don't drop good data.
func (r *ring) requeue(samples []schema.Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// If combining would overflow, drop oldest re-queued samples to
	// preserve the bound.
	total := len(samples) + len(r.data)
	if total > r.max {
		overflow := total - r.max
		if overflow >= len(samples) {
			samples = nil
		} else {
			samples = samples[overflow:]
		}
	}
	r.data = append(samples, r.data...)
}
