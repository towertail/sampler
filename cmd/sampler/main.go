package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/towertail/sampler/internal/collect"
	"github.com/towertail/sampler/internal/schema"
	"github.com/towertail/sampler/internal/version"
)

type options struct {
	once      bool
	interval  time.Duration
	ver       bool
	selfCheck bool
	noDisk    bool
	noNet     bool
	noProc    bool
	topN      int
}

func parseFlags(args []string) (*options, error) {
	fs := flag.NewFlagSet("towertail-sampler", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := &options{}
	fs.BoolVar(&opts.once, "once", false, "emit one sample and exit")
	fs.DurationVar(&opts.interval, "interval", 0, "emit NDJSON every <dur>")
	fs.BoolVar(&opts.ver, "version", false, "print version and exit")
	fs.BoolVar(&opts.selfCheck, "self-check", false, "collect one sample, print 'ok', exit 0")
	fs.BoolVar(&opts.noDisk, "no-disk", false, "skip disk collection")
	fs.BoolVar(&opts.noNet, "no-net", false, "skip net collection")
	fs.BoolVar(&opts.noProc, "no-proc", false, "skip per-process collection")
	fs.IntVar(&opts.topN, "top-n", 20, "number of top processes to return (union of top-by-CPU and top-by-RSS, deduped). 0 disables the cap.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return opts, nil
}

// buildSample collects one sample. Stateless: delta math happens inside
// the individual collectors using their own short self-sampling window.
func buildSample(opts *options, window time.Duration) schema.Sample {
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

	if !opts.noProc {
		p, errs := collect.Proc(window, opts.topN)
		allErrs = append(allErrs, errs...)
		s.Procs = &p
	}

	if allErrs == nil {
		allErrs = []string{}
	}
	s.Errors = allErrs
	return s
}

// streamingSample builds one tick's sample. For v1 simplicity, streaming
// mode uses the same short self-sampling window as one-shot; a stateful
// optimization using previous-tick counters can be added later without
// changing the schema.
func streamingSample(opts *options) schema.Sample {
	return buildSample(opts, collect.SampleWindow)
}

func writeSample(w io.Writer, s schema.Sample) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	if opts.ver {
		fmt.Fprintf(stdout, "towertail-sampler %s %s\n", version.Version, version.SHA)
		return 0
	}

	if opts.selfCheck {
		_ = buildSample(opts, 50*time.Millisecond)
		fmt.Fprintln(stdout, "ok")
		return 0
	}

	// Default to --once if no mode flag given.
	if opts.interval <= 0 {
		s := buildSample(opts, collect.SampleWindow)
		if err := writeSample(stdout, s); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	// Streaming mode.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	// Emit the first sample immediately so consumers don't have to wait
	// a full interval for initial data.
	s := streamingSample(opts)
	if err := writeSample(stdout, s); err != nil {
		return 1
	}

	for {
		select {
		case <-sig:
			return 0
		case <-ticker.C:
			s := streamingSample(opts)
			if err := writeSample(stdout, s); err != nil {
				return 1
			}
		}
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
