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
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/towertail/sampler/pkg/schema"
)

// Messages of the error lines that the runner makes itself.
const (
	msgDiskScanOff = "disk scans are off on this host: run the sampler with --allow-disk-scan"
	msgScanBusy    = "a scan already runs on this host"
)

const (
	// maxResultGzip is the server limit for the result PUT body.
	maxResultGzip = 16 << 20
	// maxScanLine is the longest line that the runner reads from a scan.
	maxScanLine = 256 << 20
	// resultRetries is the number of PUT retries after the first attempt.
	resultRetries = 3
)

// ControlMessage is one message from GET /v1/control/next.
type ControlMessage struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// ScanProcess is a started scan. Stdout gives its NDJSON lines. Stop
// asks the scan to stop: it then writes a cancelled line and ends.
type ScanProcess struct {
	Stdout io.Reader
	Stop   func()
	Wait   func() error
}

// ScanStarter starts the scan subcommand with args.
type ScanStarter func(ctx context.Context, args []string) (*ScanProcess, error)

// ExecScanStarter starts `<exe> scan …` as a subprocess. An empty exe
// is the executable of this process. The subprocess lowers its own
// priority, so the push process keeps its priority. Closing the stdin
// of the subprocess stops the scan.
func ExecScanStarter(exe string, stderr io.Writer) ScanStarter {
	return func(ctx context.Context, args []string) (*ScanProcess, error) {
		if exe == "" {
			var err error
			if exe, err = os.Executable(); err != nil {
				return nil, err
			}
		}
		cmd := exec.CommandContext(ctx, exe, args...)
		cmd.Stderr = stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		// On ctx done, close stdin first. Kill only if the scan does not stop.
		cmd.Cancel = in.Close
		cmd.WaitDelay = 10 * time.Second
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &ScanProcess{
			Stdout: out,
			Stop:   func() { _ = in.Close() },
			Wait:   cmd.Wait,
		}, nil
	}
}

// scanJob is the payload of a disk_scan message.
type scanJob struct {
	ID       string `json:"job_id"`
	Root     string `json:"root"`
	MaxDepth int    `json:"max_depth"`
	MinBytes int64  `json:"min_bytes"`
	// MinFrac is nil for the scan default. A refresh sends 0.
	MinFrac *float64 `json:"min_frac,omitempty"`
}

// args gives the scan flags. A zero value keeps the scan default.
func (j scanJob) args() []string {
	args := []string{"scan", "--progress-interval", "2s"}
	if j.Root != "" {
		args = append(args, "--root", j.Root)
	}
	if j.MaxDepth > 0 {
		args = append(args, "--max-depth", strconv.Itoa(j.MaxDepth))
	}
	if j.MinBytes > 0 {
		args = append(args, "--min-bytes", strconv.FormatInt(j.MinBytes, 10))
	}
	if j.MinFrac != nil {
		args = append(args, "--min-frac", strconv.FormatFloat(*j.MinFrac, 'g', -1, 64))
	}
	return args
}

// ScanRunner runs disk_scan jobs, one at a time, and sends their lines
// to the server.
type ScanRunner struct {
	endpoint string
	token    string
	allow    bool
	client   *http.Client
	start    ScanStarter
	stderr   io.Writer
	// backoff is the wait before the first result PUT retry. It doubles
	// for each retry.
	backoff time.Duration

	mu      sync.Mutex
	running bool
	wg      sync.WaitGroup
}

// NewScanRunner returns a runner for the server in cfg. It refuses all
// jobs when cfg.AllowDiskScan is false.
func NewScanRunner(cfg Config, client *http.Client, start ScanStarter, stderr io.Writer) *ScanRunner {
	return &ScanRunner{
		endpoint: cfg.Endpoint,
		token:    cfg.Token,
		allow:    cfg.AllowDiskScan,
		client:   client,
		start:    start,
		stderr:   stderr,
		backoff:  time.Second,
	}
}

// Handle starts the scan of a disk_scan message in a new goroutine and
// returns. It posts an error line for the job when it cannot start it.
func (r *ScanRunner) Handle(ctx context.Context, msg ControlMessage) error {
	var job scanJob
	if err := json.Unmarshal(msg.Payload, &job); err != nil {
		return fmt.Errorf("disk_scan: bad payload: %w", err)
	}
	if job.ID == "" {
		return errors.New("disk_scan: no job_id")
	}
	if !r.allow {
		return r.postError(ctx, job.ID, msgDiskScanOff)
	}
	if !r.acquire() {
		return r.postError(ctx, job.ID, msgScanBusy)
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.release()
		r.run(ctx, job)
	}()
	return nil
}

// Wait blocks until the running scan ends.
func (r *ScanRunner) Wait() { r.wg.Wait() }

func (r *ScanRunner) acquire() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

func (r *ScanRunner) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
}

// run reads the lines of one scan and sends them. Each path ends the
// job with an error, cancelled or result line, if the server is there.
func (r *ScanRunner) run(ctx context.Context, job scanJob) {
	proc, err := r.start(ctx, job.args())
	if err != nil {
		r.logErr(job.ID, r.postError(ctx, job.ID, "start the scan: "+err.Error()))
		return
	}
	var once sync.Once
	stop := func() { once.Do(proc.Stop) }
	defer stop()
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-ended:
		}
	}()

	final := false
	sc := bufio.NewScanner(proc.Stdout)
	sc.Buffer(make([]byte, 64<<10), maxScanLine)
	for sc.Scan() {
		line := sc.Bytes()
		var l schema.ScanLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		switch l.T {
		case schema.ScanLineProgress:
			cancel, err := r.postLine(ctx, job.ID, line)
			r.logErr(job.ID, err)
			if cancel {
				stop()
			}
		case schema.ScanLineError, schema.ScanLineCancelled:
			final = true
			_, err := r.postLine(ctx, job.ID, line)
			r.logErr(job.ID, err)
		case schema.ScanLineResult:
			final = true
			if err := r.putResult(ctx, job.ID, line); err != nil {
				r.logErr(job.ID, err)
				r.logErr(job.ID, r.postError(ctx, job.ID, "upload the result: "+err.Error()))
			}
		}
	}
	readErr := sc.Err()
	if readErr != nil {
		// Read the rest so that the scan does not block on a full pipe.
		stop()
		_, _ = io.Copy(io.Discard, proc.Stdout)
	}
	waitErr := proc.Wait()
	if final {
		return
	}
	cause := "no final line"
	switch {
	case readErr != nil:
		cause = readErr.Error()
	case waitErr != nil:
		cause = waitErr.Error()
	}
	r.logErr(job.ID, r.postError(ctx, job.ID, "the scan stopped with no result: "+cause))
}

func (r *ScanRunner) postError(ctx context.Context, jobID, message string) error {
	line, err := json.Marshal(schema.ScanError{T: schema.ScanLineError, V: schema.ScanVersion, Message: message})
	if err != nil {
		return err
	}
	_, err = r.postLine(ctx, jobID, line)
	return err
}

// postLine posts one progress, error or cancelled line. It returns true
// when the server wants the scan to stop. A 403 or 404 also stops the
// scan, because the job is not there for this node.
func (r *ScanRunner) postLine(ctx context.Context, jobID string, line []byte) (bool, error) {
	req, err := r.request(ctx, http.MethodPost, jobID, "/progress", line)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return true, fmt.Errorf("progress: server %d", resp.StatusCode)
	case resp.StatusCode >= 300:
		return false, fmt.Errorf("progress: server %d", resp.StatusCode)
	}
	var body struct {
		Cancel bool `json:"cancel"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("progress: %w", err)
	}
	return body.Cancel, nil
}

// putResult gzips the result line in memory and PUTs it. It retries
// network errors, 429 and 5xx resultRetries times with backoff.
func (r *ScanRunner) putResult(ctx context.Context, jobID string, line []byte) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(line); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if buf.Len() > maxResultGzip {
		return fmt.Errorf("the result is %d bytes after gzip, more than 16 MiB", buf.Len())
	}
	body := buf.Bytes()

	wait := r.backoff
	var err error
	for attempt := 0; attempt <= resultRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		var retry bool
		retry, err = r.putOnce(ctx, jobID, body)
		if err == nil || !retry {
			return err
		}
		fmt.Fprintf(r.stderr, "push: disk scan %s: result attempt %d: %v\n", jobID, attempt+1, err)
	}
	return err
}

// putOnce sends the result one time. It returns true when a retry can help.
func (r *ScanRunner) putOnce(ctx context.Context, jobID string, body []byte) (bool, error) {
	req, err := r.request(ctx, http.MethodPut, jobID, "/result", body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := r.client.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Errorf("result: server %d", resp.StatusCode)
	default:
		return false, fmt.Errorf("result: server %d", resp.StatusCode)
	}
}

func (r *ScanRunner) request(ctx context.Context, method, jobID, suffix string, body []byte) (*http.Request, error) {
	u := joinURL(r.endpoint, "/v1/disk-scans/"+url.PathEscape(jobID)+suffix)
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	return req, nil
}

func (r *ScanRunner) logErr(jobID string, err error) {
	if err != nil {
		fmt.Fprintf(r.stderr, "push: disk scan %s: %v\n", jobID, err)
	}
}
