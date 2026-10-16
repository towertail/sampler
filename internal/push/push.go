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
}

// SampleFn produces a fresh Sample. Injected so the sampler can reuse
// its own buildSample without push importing collect.
type SampleFn func() schema.Sample

// Run collects until ctx is cancelled. Returns when the context is
// done or a fatal (unrecoverable) error occurs.
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

	newPusher(cfg, stderr).run(ctx, sampleFn)
	return nil
}

// run starts the goroutines and waits for them and for a running scan.
func (p *pusher) run(ctx context.Context, sampleFn SampleFn) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.collect(ctx, sampleFn) }()

	wg.Add(1)
	go func() { defer wg.Done(); p.flush(ctx) }()

	if p.cfg.AllowControl || p.cfg.AllowDiskScan {
		wg.Add(1)
		go func() { defer wg.Done(); p.heartbeat(ctx) }()
	}

	wg.Wait()
	p.scans.Wait()
}

// authLogEvery limits the auth-rejected log line so a bad token does
// not fill the journal.
const authLogEvery = time.Minute

type pusher struct {
	cfg    Config
	client *http.Client
	ring   *ring
	scans  *ScanRunner
	stderr io.Writer

	now         func() time.Time
	lastAuthLog time.Time
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
	}
}

// collect ticks at cfg.Interval and pushes one sample onto the ring.
func (p *pusher) collect(ctx context.Context, sampleFn SampleFn) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	// Emit the first sample immediately so downstream sees data < 1 interval.
	p.ring.push(sampleFn())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.ring.push(sampleFn())
		}
	}
}

// flush attempts to POST every cfg.FlushInterval or whenever
// cfg.BatchSize is reached.
func (p *pusher) flush(ctx context.Context) {
	t := time.NewTicker(p.cfg.FlushInterval)
	defer t.Stop()
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			// Best-effort final flush.
			_ = p.attemptFlush(context.Background())
			return
		case <-t.C:
			if err := p.attemptFlush(ctx); err != nil {
				fmt.Fprintf(p.stderr, "push: flush error: %v\n", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			} else {
				backoff = time.Second
			}
		}
	}
}

func (p *pusher) attemptFlush(ctx context.Context) error {
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
	resp, err := p.client.Do(req)
	if err != nil {
		p.ring.requeue(batch)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		p.ring.requeue(batch)
		return fmt.Errorf("server %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// A retry cannot fix a bad token, so the batch stays dropped.
		p.logAuthRejected(resp.StatusCode)
		return nil
	}
	if resp.StatusCode >= 400 {
		// Schema-rejection etc. — drop to avoid poison loop.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		fmt.Fprintf(p.stderr, "push: server %d: %s\n", resp.StatusCode, strings.TrimSpace(string(b)))
		return nil
	}
	return nil
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
	var msg ControlMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return
	}
	switch msg.Kind {
	case "disk_scan":
		if err := p.scans.Handle(ctx, msg); err != nil {
			fmt.Fprintf(p.stderr, "push: disk_scan: %v\n", err)
		}
	case "kill_process":
		if !p.cfg.AllowControl {
			fmt.Fprintln(p.stderr, "push: kill_process: refused, --allow-control is off")
			return
		}
		var payload struct {
			PID int32 `json:"pid"`
		}
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
