package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/towertail/sampler/internal/boot"
	"github.com/towertail/sampler/internal/collect"
	"github.com/towertail/sampler/internal/daemon"
	"github.com/towertail/sampler/internal/push"
	"github.com/towertail/sampler/internal/version"
)

const (
	logMax       = 5 << 20
	stopWait     = 10 * time.Second
	lockWait     = time.Second
	defaultEvery = 30 * time.Second
)

var errUnsupported = &exitError{code: 2, msg: "unsupported on windows"}

// bootDisabler removes the reboot entry. The push loop calls it on 410.
type bootDisabler interface {
	Disable(ctx context.Context) error
}

// bootManager is the reboot entry as the commands use it. *boot.Entry
// satisfies it. Tests give a fake.
type bootManager interface {
	bootDisabler
	Enable(ctx context.Context, mode string) (string, error)
	Detect(ctx context.Context) string
	StartService(ctx context.Context) error
	StopService(ctx context.Context) error
}

var (
	// startWait is the time that start waits for the process to take the lock.
	startWait = 10 * time.Second
	// detach starts the sampler process in the background. Tests replace it.
	detach = daemon.Detach
	// joinCall and machineID are replaced in tests.
	joinCall  = push.Join
	machineID = collect.MachineID
	newBoot   = func(h daemon.Home) bootManager { return boot.NewEntry(h, boot.ExecRunner{}) }
)

func startFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "endpoint", Usage: "server base URL"},
		&cli.StringFlag{Name: "token-file", Usage: "file with the sampler token, or - for one line of stdin"},
		&cli.StringFlag{Name: "join", Usage: "one-use join token: trade it for a sampler token"},
		&cli.StringFlag{Name: "name", Usage: "host name for --join (default: the host name)"},
		&cli.DurationFlag{Name: "interval", Value: defaultEvery, Usage: "sample cadence"},
		&cli.BoolFlag{Name: "allow-control", Usage: "allow server-issued control messages (kill_process)"},
		&cli.BoolFlag{Name: "allow-disk-scan", Usage: "allow server-issued disk scans (disk_scan)"},
		&cli.BoolFlag{Name: "insecure", Usage: "accept self-signed TLS certs"},
		&cli.BoolFlag{Name: "no-proc-cwd", Usage: "do not send the working folder and repo of processes"},
		&cli.StringFlag{Name: "boot", Usage: "reboot entry: auto, linger, launchagent, cron or none (default: the saved one, else auto)"},
		&cli.BoolFlag{Name: "foreground", Usage: "run in this process and not in the background"},
		homeFlag(),
	}
}

func homeFlag() cli.Flag {
	return &cli.StringFlag{Name: "home", Hidden: true, Usage: "sampler folder (default ~/.towertail)"}
}

// runStart saves the config when flags are given, sets the reboot entry
// and starts the sampler in the background.
func runStart(ctx context.Context, c *cli.Command) error {
	if runtime.GOOS == "windows" {
		return errUnsupported
	}
	h := daemon.NewHome(c.String("home"))
	cfg, token, err := loadStartConfig(ctx, c, h)
	if err != nil {
		return err
	}
	b := newBoot(h)
	cfg = ensureBoot(ctx, c, h, b, cfg)
	if c.Bool("foreground") {
		return runForeground(ctx, c, h, b, cfg, token)
	}
	return startBackground(ctx, c, h, b, cfg)
}

// loadStartConfig saves the config and the token from the flags. Without
// flags it loads the saved ones.
func loadStartConfig(ctx context.Context, c *cli.Command, h daemon.Home) (daemon.StartConfig, string, error) {
	saved, savedErr := h.LoadConfig()
	if !c.IsSet("endpoint") && !c.IsSet("token-file") && !c.IsSet("join") {
		token, tokErr := h.LoadToken()
		if errors.Is(savedErr, os.ErrNotExist) || errors.Is(tokErr, os.ErrNotExist) {
			return saved, "", &exitError{code: 2, msg: "not set up: run start with --endpoint and --token-file"}
		}
		if savedErr != nil {
			return saved, "", savedErr
		}
		if tokErr == nil && c.IsSet("no-proc-cwd") && saved.NoProcCwd != c.Bool("no-proc-cwd") {
			saved.NoProcCwd = c.Bool("no-proc-cwd")
			if err := h.SaveConfig(saved); err != nil {
				return saved, "", err
			}
		}
		return saved, token, tokErr
	}
	cfg := daemon.StartConfig{
		Endpoint:      c.String("endpoint"),
		Interval:      c.Duration("interval"),
		AllowControl:  c.Bool("allow-control"),
		AllowDiskScan: c.Bool("allow-disk-scan"),
		Insecure:      c.Bool("insecure"),
	}
	if savedErr == nil {
		cfg.Boot = saved.Boot
		cfg.NoProcCwd = saved.NoProcCwd
		if cfg.Endpoint == "" {
			cfg.Endpoint = saved.Endpoint
		}
	}
	if c.IsSet("no-proc-cwd") {
		cfg.NoProcCwd = c.Bool("no-proc-cwd")
	}
	if cfg.Endpoint == "" {
		return cfg, "", &exitError{code: 2, msg: "--endpoint required"}
	}
	var (
		token string
		err   error
	)
	if c.IsSet("join") {
		if c.IsSet("token-file") {
			return cfg, "", &exitError{code: 2, msg: "use --join or --token-file, not both"}
		}
		token, err = joinHost(ctx, c, &cfg)
	} else {
		token, err = resolveToken(c, stdin)
	}
	if err != nil {
		return cfg, "", &exitError{code: 2, msg: err.Error()}
	}
	if err := h.SaveConfig(cfg); err != nil {
		return cfg, "", err
	}
	if err := h.SaveToken(token); err != nil {
		return cfg, "", err
	}
	return cfg, token, nil
}

// joinHost trades the join token for a sampler token. A new endpoint
// from the server replaces the one in cfg.
func joinHost(ctx context.Context, c *cli.Command, cfg *daemon.StartConfig) (string, error) {
	name := c.String("name")
	if name == "" {
		name, _ = os.Hostname()
	}
	if name == "" {
		return "", errors.New("--name required: the host has no name")
	}
	enr, err := joinCall(ctx, cfg.Endpoint, c.String("join"), name, machineID(), cfg.Insecure)
	switch {
	case errors.Is(err, push.ErrJoinRejected):
		return "", errors.New("join token rejected: make a new one")
	case errors.Is(err, push.ErrPlanFull):
		return "", errors.New("plan is full: the plan has no room for this host")
	case err != nil:
		return "", err
	}
	if enr.Endpoint != "" {
		cfg.Endpoint = enr.Endpoint
	}
	return enr.Token, nil
}

// ensureBoot sets the reboot entry when the wanted mode differs from the
// saved one. A failure prints a warning and the start goes on.
func ensureBoot(ctx context.Context, c *cli.Command, h daemon.Home, b bootManager, cfg daemon.StartConfig) daemon.StartConfig {
	want := c.String("boot")
	if want == "" {
		want = cfg.Boot
	}
	if want == "" {
		want = "auto"
	}
	have := cfg.Boot != "" && cfg.Boot != "none"
	var (
		got string
		err error
	)
	switch {
	case want == "none" && have && c.IsSet("boot"):
		err = b.Disable(ctx)
		got = "none"
	case want != "none" && (!have || want != "auto" && want != cfg.Boot):
		got, err = b.Enable(ctx, want)
	default:
		return cfg
	}
	if err != nil {
		fmt.Fprintf(c.Root().ErrWriter, "warning: reboot entry: %v\n", err)
		return cfg
	}
	cfg.Boot = got
	if err := h.SaveConfig(cfg); err != nil {
		fmt.Fprintf(c.Root().ErrWriter, "warning: save config: %v\n", err)
	}
	return cfg
}

func startBackground(ctx context.Context, c *cli.Command, h daemon.Home, b bootManager, cfg daemon.StartConfig) error {
	if pid, ok := h.Running(); ok {
		fmt.Fprintf(c.Root().Writer, "already running, pid %d\n", pid)
		return nil
	}
	if strings.Contains(cfg.Boot, "linger") {
		err := b.StartService(ctx)
		if err == nil {
			var pid int
			if pid, err = h.WaitRunning(startWait); err == nil {
				fmt.Fprintf(c.Root().Writer, "started, pid %d\n", pid)
				return nil
			}
		}
		fmt.Fprintf(c.Root().ErrWriter, "warning: systemd start: %v\n", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"start", "--foreground"}
	if c.IsSet("home") {
		args = append(args, "--home", h.Dir)
	}
	pid, err := detach(exe, args, h, startWait)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Root().Writer, "started, pid %d\n", pid)
	return nil
}

// runForeground holds the lock, writes the pid and the log, and runs the
// push loop until a signal or an exit order.
func runForeground(ctx context.Context, c *cli.Command, h daemon.Home, b bootManager, cfg daemon.StartConfig, token string) error {
	lock, err := h.LockWait(lockWait)
	if errors.Is(err, daemon.ErrRunning) {
		pid, _ := h.Running()
		fmt.Fprintf(c.Root().Writer, "already running, pid %d\n", pid)
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := h.WritePID(os.Getpid()); err != nil {
		return err
	}
	defer h.RemovePID()
	logf, err := daemon.OpenRotating(h.LogPath(), logMax)
	if err != nil {
		return err
	}
	defer logf.Close()
	log := daemon.Stamp(logf)

	state := h.StateFile()
	if err := state.Reset(os.Getpid(), version.String(), cfg.Endpoint); err != nil {
		fmt.Fprintf(log, "state: %v\n", err)
	}
	fmt.Fprintf(log, "start: sampler %s, pid %d\n", version.String(), os.Getpid())
	if cfg.Interval <= 0 {
		cfg.Interval = defaultEvery
	}
	pcfg := push.Config{
		Endpoint:      cfg.Endpoint,
		Token:         token,
		Interval:      cfg.Interval,
		AllowControl:  cfg.AllowControl,
		AllowDiskScan: cfg.AllowDiskScan,
		Insecure:      cfg.Insecure,
		Recorder:      state,
		UserAgent:     userAgent(),
	}
	opts := options{
		topN:          20,
		procScanMax:   collect.DefaultProcScanMax,
		portsInterval: collect.DefaultPortsInterval,
		portsMax:      collect.DefaultPortsMaxConn,
		noProcCwd:     cfg.NoProcCwd,
	}
	pc := &portCache{}
	sw := newProcCwdSwitch(opts.noProcCwd, h.SetNoProcCwd, log)
	pcfg.OnProcCwd = sw.Apply
	err = runPush(ctx, pcfg, sw.sampleFn(opts, pc), h, b, log)
	fmt.Fprintln(log, "stop")
	return err
}

func userAgent() string { return "towertail-sampler/" + version.Version }

// runPush is the loop of `push` and `start --foreground`. A SIGTERM or
// SIGINT stops it after the final flush. On an exit order (410) it
// removes the reboot entry and the saved config and returns nil.
func runPush(ctx context.Context, cfg push.Config, sampleFn push.SampleFn, h daemon.Home, b bootDisabler, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := push.Run(ctx, cfg, sampleFn, stderr)
	var gone *push.GoneError
	if !errors.As(err, &gone) {
		return err
	}
	fmt.Fprintf(stderr, "cloud asked to exit: %s\n", gone.Reason)
	if b != nil {
		if err := b.Disable(context.WithoutCancel(ctx)); err != nil {
			fmt.Fprintf(stderr, "remove reboot entry: %v\n", err)
		}
	}
	if err := h.Forget(); err != nil {
		fmt.Fprintf(stderr, "forget config: %v\n", err)
	}
	return nil
}

// runStop stops the running sampler. In linger mode it stops the unit
// first.
func runStop(ctx context.Context, c *cli.Command) error {
	if runtime.GOOS == "windows" {
		return errUnsupported
	}
	h := daemon.NewHome(c.String("home"))
	if _, ok := h.Running(); !ok {
		fmt.Fprintln(c.Root().Writer, "not running")
		return nil
	}
	if cfg, err := h.LoadConfig(); err == nil && strings.Contains(cfg.Boot, "linger") {
		if err := newBoot(h).StopService(ctx); err != nil {
			fmt.Fprintf(c.Root().ErrWriter, "warning: systemd stop: %v\n", err)
		}
	}
	if _, err := h.Terminate(stopWait); err != nil {
		return err
	}
	fmt.Fprintln(c.Root().Writer, "stopped")
	return nil
}

// runStatus prints the state as one JSON line. `running` comes from the
// lock. It exits 0 also when the sampler does not run.
func runStatus(ctx context.Context, c *cli.Command) error {
	h := daemon.NewHome(c.String("home"))
	st, err := h.StateFile().Read()
	if err != nil {
		st = daemon.State{}
	}
	pid, running := h.Running()
	st.PID = pid
	if !running || st.Version == "" {
		st.Version = version.String()
	}
	st.Reboot = newBoot(h).Detect(ctx)
	return writeJSON(c.Root().Writer, daemon.Status{Running: running, State: st})
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return &exitError{code: 1, msg: err.Error()}
	}
	return nil
}

// resolveToken pulls a sampler bearer from --token, then --token-file
// (- reads one line of stdin), then TOWERTAIL_TOKEN. It returns an error
// if none is set or the token is empty.
func resolveToken(c *cli.Command, stdin io.Reader) (string, error) {
	if v := c.String("token"); v != "" {
		return v, nil
	}
	p := c.String("token-file")
	if p == "" {
		return "", fmt.Errorf("--token or --token-file required (or TOWERTAIL_TOKEN env)")
	}
	var raw string
	if p == "-" {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		raw = line
	} else {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		raw = string(b)
	}
	tok := strings.TrimSpace(raw)
	if tok == "" {
		return "", errors.New("empty token")
	}
	return tok, nil
}

func bootCommand() *cli.Command {
	return &cli.Command{
		Name:  "boot",
		Usage: "set, remove or show the entry that starts the sampler after a reboot",
		Commands: []*cli.Command{
			{
				Name:  "enable",
				Usage: "set the reboot entry",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "mode", Value: "auto", Usage: "auto, linger, launchagent or cron"},
					homeFlag(),
				},
				Action: runBootEnable,
			},
			{Name: "disable", Usage: "remove the reboot entry", Flags: []cli.Flag{homeFlag()}, Action: runBootDisable},
			{Name: "show", Usage: "show the reboot entry that works", Flags: []cli.Flag{homeFlag()}, Action: runBootShow},
		},
	}
}

type rebootLine struct {
	Reboot string `json:"reboot"`
}

// bootFail prints the error as a JSON line and returns exit code 1.
func bootFail(c *cli.Command, err error) error {
	_ = writeJSON(c.Root().Writer, map[string]string{"error": err.Error()})
	return &exitError{code: 1, msg: err.Error()}
}

func runBootEnable(ctx context.Context, c *cli.Command) error {
	if runtime.GOOS == "windows" {
		return errUnsupported
	}
	h := daemon.NewHome(c.String("home"))
	got, err := newBoot(h).Enable(ctx, c.String("mode"))
	if err != nil {
		return bootFail(c, err)
	}
	if err := h.SetBoot(got); err != nil {
		fmt.Fprintf(c.Root().ErrWriter, "warning: save config: %v\n", err)
	}
	return writeJSON(c.Root().Writer, rebootLine{got})
}

func runBootDisable(ctx context.Context, c *cli.Command) error {
	if runtime.GOOS == "windows" {
		return errUnsupported
	}
	h := daemon.NewHome(c.String("home"))
	if err := newBoot(h).Disable(ctx); err != nil {
		return bootFail(c, err)
	}
	if err := h.SetBoot("none"); err != nil {
		fmt.Fprintf(c.Root().ErrWriter, "warning: save config: %v\n", err)
	}
	return writeJSON(c.Root().Writer, rebootLine{"none"})
}

func runBootShow(ctx context.Context, c *cli.Command) error {
	h := daemon.NewHome(c.String("home"))
	return writeJSON(c.Root().Writer, rebootLine{newBoot(h).Detect(ctx)})
}
