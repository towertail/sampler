package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/towertail/sampler/internal/collect"
	"github.com/towertail/sampler/internal/push"
	"github.com/towertail/sampler/pkg/schema"
	"github.com/towertail/sampler/internal/svc"
	"github.com/towertail/sampler/internal/version"
)

type options struct {
	once          bool
	interval      time.Duration
	ver           bool
	selfCheck     bool
	noDisk        bool
	noNet         bool
	noProc        bool
	noPorts       bool
	noHealth      bool
	topN          int
	procScanMax   int
	maxRuntime    time.Duration
	portsInterval time.Duration
	portsMax      int
}

// portCache holds the last ports snapshot in streaming mode so the
// sampler can re-emit it on intermediate ticks. The collector is the
// most expensive thing the sampler does on Linux (walks /proc/<pid>/fd/*),
// so refreshing every 10s instead of every 1s tick is a 10x cost cut on
// busy hosts. Stale-but-present is better than missing — the client
// reads `collected_ts` to render staleness.
type portCache struct {
	last         *schema.PortList
	lastErrs     []string
	lastRefresh  time.Time
}

func (pc *portCache) get(now time.Time, every time.Duration, max int) (*schema.PortList, []string) {
	if pc.last == nil || now.Sub(pc.lastRefresh) >= every {
		p, errs := collect.Ports(max)
		pc.last = &p
		pc.lastErrs = errs
		pc.lastRefresh = now
	}
	return pc.last, pc.lastErrs
}

// run is kept as the legacy test surface (main_test.go calls it).
// urfave/cli replaces argument parsing but still dispatches into the
// same sample-building code paths below.
func run(args []string, stdout io.Writer, stderr io.Writer) int {
	app := newCLI(stdout, stderr)
	if err := app.Run(context.Background(), append([]string{"towertail-sampler"}, args...)); err != nil {
		fmt.Fprintln(stderr, err)
		if ee, ok := err.(*exitError); ok {
			return ee.code
		}
		return 1
	}
	return 0
}

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// exitError is thrown by subcommands that want a specific exit code.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func newCLI(stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:    "towertail-sampler",
		Usage:   "Towertail host metrics sampler",
		Version: fmt.Sprintf("%s %s", version.Version, version.SHA),
		Writer:  stdout,
		ErrWriter: stderr,
		// Legacy top-level flags — preserved so the Mac-side bootstrap
		// that invokes `towertail-sampler --version`, `--once`, and
		// `--self-check` over SSH keeps working untouched.
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "once", Usage: "emit one sample and exit"},
			&cli.DurationFlag{Name: "interval", Usage: "emit NDJSON every <dur>"},
			&cli.BoolFlag{Name: "version", Aliases: []string{"V"}, Usage: "print version and exit"},
			&cli.BoolFlag{Name: "self-check", Usage: "verify the binary runs on this host"},
			&cli.BoolFlag{Name: "no-disk", Usage: "skip disk collection"},
			&cli.BoolFlag{Name: "no-net", Usage: "skip net collection"},
			&cli.BoolFlag{Name: "no-proc", Usage: "skip per-process collection"},
			&cli.BoolFlag{Name: "no-ports", Usage: "skip per-process ports collection"},
			&cli.BoolFlag{Name: "no-health", Usage: "skip host health collection"},
			&cli.IntFlag{Name: "top-n", Value: 20, Usage: "top-N cap for process list"},
			&cli.IntFlag{Name: "proc-scan-max", Value: collect.DefaultProcScanMax, Usage: "skip the per-process scan above this process count (0 = no limit)"},
			&cli.DurationFlag{Name: "ports-interval", Value: collect.DefaultPortsInterval, Usage: "ports refresh cadence in streaming mode"},
			&cli.IntFlag{Name: "ports-max", Value: collect.DefaultPortsMaxConn, Usage: "max connections enumerated per ports refresh"},
			&cli.DurationFlag{Name: "max-runtime", Usage: "exit after this duration (streaming modes only; 0 = no limit)"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runRoot(ctx, c, stdout, stderr)
		},
		Commands: []*cli.Command{
			{
				Name:  "once",
				Usage: "emit one sample and exit",
				Flags: metricGatingFlags(),
				Action: func(ctx context.Context, c *cli.Command) error {
					opts := gatingFromCmd(c)
					opts.once = true
					return runOnce(opts, stdout, stderr)
				},
			},
			{
				Name:  "stream",
				Usage: "emit NDJSON at --interval",
				Flags: append(metricGatingFlags(),
					&cli.DurationFlag{Name: "interval", Value: time.Second, Usage: "tick duration"},
					&cli.DurationFlag{Name: "max-runtime", Usage: "exit after this duration (0 = no limit)"},
				),
				Action: func(ctx context.Context, c *cli.Command) error {
					opts := gatingFromCmd(c)
					opts.interval = c.Duration("interval")
					opts.maxRuntime = c.Duration("max-runtime")
					return runStream(ctx, opts, stdout, stderr)
				},
			},
			{
				Name:  "push",
				Usage: "collect samples and push to a Towertail server",
				Flags: append(metricGatingFlags(),
					&cli.StringFlag{Name: "endpoint", Usage: "server base URL", Required: true, Sources: cli.EnvVars("TOWERTAIL_ENDPOINT")},
					&cli.StringFlag{Name: "token", Usage: "sampler bearer token", Sources: cli.EnvVars("TOWERTAIL_TOKEN")},
					&cli.StringFlag{Name: "token-file", Usage: "file containing the bearer token"},
					&cli.DurationFlag{Name: "interval", Value: 30 * time.Second, Usage: "sample cadence"},
					&cli.DurationFlag{Name: "flush", Value: 5 * time.Second, Usage: "max flush period"},
					&cli.IntFlag{Name: "batch", Value: 10, Usage: "max samples per batch"},
					&cli.IntFlag{Name: "buffer", Value: 1000, Usage: "ring buffer size"},
					&cli.BoolFlag{Name: "allow-control", Usage: "allow server-issued control messages (kill_process)"},
					&cli.DurationFlag{Name: "heartbeat", Value: 15 * time.Second, Usage: "control long-poll cadence"},
					&cli.BoolFlag{Name: "insecure", Usage: "accept self-signed TLS certs"},
				),
				Action: func(ctx context.Context, c *cli.Command) error {
					opts := gatingFromCmd(c)
					opts.interval = c.Duration("interval")
					token, err := resolveToken(c)
					if err != nil {
						return err
					}
					pcfg := push.Config{
						Endpoint:      c.String("endpoint"),
						Token:         token,
						Interval:      opts.interval,
						FlushInterval: c.Duration("flush"),
						BatchSize:     c.Int("batch"),
						Buffer:        c.Int("buffer"),
						AllowControl:  c.Bool("allow-control"),
						Heartbeat:     c.Duration("heartbeat"),
						Insecure:      c.Bool("insecure"),
					}
					// Push mode is a long-lived loop too — use a port cache so
					// the expensive ports collector runs every ports-interval
					// rather than every sample.
					pc := &portCache{}
					return push.Run(ctx, pcfg, func() schema.Sample {
						return streamingSample(&opts, pc)
					}, stderr)
				},
			},
			{
				Name:  "service",
				Usage: "manage the sampler as a background service",
				Commands: []*cli.Command{
					{Name: "install", Flags: serviceInstallFlags(), Action: runServiceInstall},
					{Name: "uninstall", Action: runServiceUninstall},
					{Name: "start", Action: runServiceStart},
					{Name: "stop", Action: runServiceStop},
					{Name: "status", Action: runServiceStatus},
					{Name: "log", Flags: []cli.Flag{&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}}}, Action: runServiceLog},
				},
			},
			{
				Name:  "version",
				Usage: "print version",
				Action: func(ctx context.Context, c *cli.Command) error {
					fmt.Fprintf(stdout, "towertail-sampler %s %s\n", version.Version, version.SHA)
					return nil
				},
			},
			{
				Name:  "self-check",
				Usage: "collect one sample, throw it away, print 'ok'",
				Action: func(ctx context.Context, c *cli.Command) error {
					opts := options{procScanMax: collect.DefaultProcScanMax}
					// self-check skips ports — it's a fast "does this binary run" probe,
		// not a full collection.
		opts.noPorts = true
		_ = buildSample(&opts, 50*time.Millisecond, nil)
					fmt.Fprintln(stdout, "ok")
					return nil
				},
			},
		},
	}
}

// metricGatingFlags are the shared --no-disk / --no-net / --no-proc /
// --no-ports / --top-n / --ports-* flags used by the collect subcommands.
func metricGatingFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{Name: "no-disk"},
		&cli.BoolFlag{Name: "no-net"},
		&cli.BoolFlag{Name: "no-proc"},
		&cli.BoolFlag{Name: "no-ports"},
		&cli.BoolFlag{Name: "no-health"},
		&cli.IntFlag{Name: "top-n", Value: 20},
		&cli.IntFlag{Name: "proc-scan-max", Value: collect.DefaultProcScanMax},
		&cli.DurationFlag{Name: "ports-interval", Value: collect.DefaultPortsInterval},
		&cli.IntFlag{Name: "ports-max", Value: collect.DefaultPortsMaxConn},
	}
}

func gatingFromCmd(c *cli.Command) options {
	return options{
		noDisk:        c.Bool("no-disk"),
		noNet:         c.Bool("no-net"),
		noProc:        c.Bool("no-proc"),
		noPorts:       c.Bool("no-ports"),
		noHealth:      c.Bool("no-health"),
		topN:          c.Int("top-n"),
		procScanMax:   c.Int("proc-scan-max"),
		portsInterval: c.Duration("ports-interval"),
		portsMax:      c.Int("ports-max"),
	}
}

// runRoot dispatches the legacy top-level flag modes. If none match it
// defaults to --once (matches the docs/sampler.md §3.1 contract).
func runRoot(ctx context.Context, c *cli.Command, stdout, stderr io.Writer) error {
	// --version / --self-check short-circuit even when other flags are set.
	if c.Bool("version") {
		fmt.Fprintf(stdout, "towertail-sampler %s %s\n", version.Version, version.SHA)
		return nil
	}
	if c.Bool("self-check") {
		opts := options{procScanMax: collect.DefaultProcScanMax}
		// self-check skips ports — it's a fast "does this binary run" probe,
		// not a full collection.
		opts.noPorts = true
		_ = buildSample(&opts, 50*time.Millisecond, nil)
		fmt.Fprintln(stdout, "ok")
		return nil
	}
	opts := options{
		once:          c.Bool("once"),
		interval:      c.Duration("interval"),
		noDisk:        c.Bool("no-disk"),
		noNet:         c.Bool("no-net"),
		noProc:        c.Bool("no-proc"),
		noPorts:       c.Bool("no-ports"),
		noHealth:      c.Bool("no-health"),
		topN:          c.Int("top-n"),
		procScanMax:   c.Int("proc-scan-max"),
		maxRuntime:    c.Duration("max-runtime"),
		portsInterval: c.Duration("ports-interval"),
		portsMax:      c.Int("ports-max"),
	}
	if opts.interval > 0 {
		return runStream(ctx, opts, stdout, stderr)
	}
	return runOnce(opts, stdout, stderr)
}

func runOnce(opts options, stdout, stderr io.Writer) error {
	// One-shot: always do a fresh ports scan, no cache. The
	// --ports-interval flag is meaningful only in streaming mode.
	s := buildSample(&opts, collect.SampleWindow, nil)
	return writeSample(stdout, s)
}

func runStream(ctx context.Context, opts options, stdout, stderr io.Writer) error {
	if opts.interval <= 0 {
		opts.interval = time.Second
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watchdog: if the parent (an SSH session) dies, sshd closes our
	// stdin. Without this, we'd only notice the disconnect on the next
	// stdout write — and if the controlling pipe goes half-open the
	// process can linger indefinitely. A goroutine reading stdin returns
	// on EOF / read error and cancels the context, so the loop exits.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()

	// Optional self-imposed lifetime cap. Bounds the worst case if the
	// stdin watchdog never trips (e.g. some sshd configs that don't
	// close the pipe promptly).
	if opts.maxRuntime > 0 {
		t := time.AfterFunc(opts.maxRuntime, cancel)
		defer t.Stop()
	}

	// Per-process ports refresh out-of-band from the main tick (default
	// every 10s). Cache the snapshot and re-emit it unchanged on
	// intermediate ticks so a freshly-attached client gets data within
	// one tick instead of waiting up to ports-interval.
	pc := &portCache{}

	// Emit the first sample immediately.
	s := streamingSample(&opts, pc)
	if err := writeSample(stdout, s); err != nil {
		return err
	}
	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sig:
			return nil
		case <-ticker.C:
			s := streamingSample(&opts, pc)
			if err := writeSample(stdout, s); err != nil {
				return err
			}
		}
	}
}

func buildSample(opts *options, window time.Duration, pc *portCache) schema.Sample {
	var allErrs []string

	h, errs := collect.Host()
	allErrs = append(allErrs, errs...)

	c, errs := collect.CPU(window)
	allErrs = append(allErrs, errs...)

	m, sw, errs := collect.Mem()
	allErrs = append(allErrs, errs...)

	s := schema.Sample{
		V:    schema.SchemaVersion,
		TS:   schema.FormatTS(time.Now()),
		Host: h,
		CPU:  c,
		Mem:  m,
		Swap: sw,
	}

	if !opts.noDisk {
		ds, errs := collect.Disk()
		allErrs = append(allErrs, errs...)
		s.Disks = &ds

		io, errs := collect.DiskIO(window)
		allErrs = append(allErrs, errs...)
		s.DiskIO = &io
	}

	if !opts.noNet {
		n, errs := collect.Net(window)
		allErrs = append(allErrs, errs...)
		s.Net = &n
	}

	if !opts.noHealth {
		h, errs := collect.Health()
		allErrs = append(allErrs, errs...)
		s.Health = &h
	}

	if !opts.noProc {
		p, errs := collect.Proc(window, opts.topN, opts.procScanMax)
		allErrs = append(allErrs, errs...)
		s.Procs = &p
	}

	if !opts.noPorts {
		max := opts.portsMax
		if max <= 0 {
			max = collect.DefaultPortsMaxConn
		}
		if pc != nil {
			every := opts.portsInterval
			if every <= 0 {
				every = collect.DefaultPortsInterval
			}
			p, errs := pc.get(time.Now(), every, max)
			allErrs = append(allErrs, errs...)
			s.Ports = p
		} else {
			p, errs := collect.Ports(max)
			allErrs = append(allErrs, errs...)
			s.Ports = &p
		}
	}

	if allErrs == nil {
		allErrs = []string{}
	}
	s.Errors = allErrs
	return s
}

func streamingSample(opts *options, pc *portCache) schema.Sample {
	return buildSample(opts, collect.SampleWindow, pc)
}

func writeSample(w io.Writer, s schema.Sample) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}

// resolveToken pulls a sampler bearer from --token, then --token-file,
// then TOWERTAIL_TOKEN. Returns an error if none are set.
func resolveToken(c *cli.Command) (string, error) {
	if v := c.String("token"); v != "" {
		return v, nil
	}
	if p := c.String("token-file"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return trimNewlines(string(b)), nil
	}
	return "", fmt.Errorf("--token or --token-file required (or TOWERTAIL_TOKEN env)")
}

func trimNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// -------- service subcommand plumbing --------

func serviceInstallFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "endpoint", Usage: "server URL", Required: true},
		&cli.StringFlag{Name: "token", Usage: "sampler bearer token", Required: true},
		&cli.StringFlag{Name: "user", Usage: "user to run the service as (linux)"},
		&cli.DurationFlag{Name: "interval", Value: 30 * time.Second},
		&cli.BoolFlag{Name: "system", Usage: "install as system service instead of per-user"},
		&cli.BoolFlag{Name: "allow-control", Usage: "allow server-issued kill_process"},
	}
}

func serviceManager(c *cli.Command) (svc.Manager, error) {
	return svc.NewManager(svc.Options{
		System: c.Bool("system"),
	})
}

func runServiceInstall(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return m.Install(ctx, svc.InstallConfig{
		ExecPath:     exe,
		Endpoint:     c.String("endpoint"),
		Token:        c.String("token"),
		User:         c.String("user"),
		Interval:     c.Duration("interval"),
		AllowControl: c.Bool("allow-control"),
	})
}

func runServiceUninstall(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	return m.Uninstall(ctx)
}

func runServiceStart(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	return m.Start(ctx)
}

func runServiceStop(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	return m.Stop(ctx)
}

func runServiceStatus(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	st, err := m.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Writer, st)
	return nil
}

func runServiceLog(ctx context.Context, c *cli.Command) error {
	m, err := serviceManager(c)
	if err != nil {
		return err
	}
	return m.Log(ctx, c.Bool("follow"), c.Writer)
}
