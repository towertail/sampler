//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/towertail/sampler/internal/daemon"
	"github.com/towertail/sampler/internal/push"
	"github.com/towertail/sampler/pkg/schema"
)

// fakeBoot records the calls of the reboot entry.
type fakeBoot struct {
	enabled     []string
	disabled    int
	enableGot   string
	enableErr   error
	detect      string
	serviceUsed []string
}

func (f *fakeBoot) Enable(ctx context.Context, mode string) (string, error) {
	f.enabled = append(f.enabled, mode)
	return f.enableGot, f.enableErr
}
func (f *fakeBoot) Disable(ctx context.Context) error { f.disabled++; return nil }
func (f *fakeBoot) Detect(ctx context.Context) string { return f.detect }
func (f *fakeBoot) StartService(ctx context.Context) error {
	f.serviceUsed = append(f.serviceUsed, "start")
	return nil
}
func (f *fakeBoot) StopService(ctx context.Context) error {
	f.serviceUsed = append(f.serviceUsed, "stop")
	return nil
}

// setBoot replaces the reboot entry for one test.
func setBoot(t *testing.T, f *fakeBoot) {
	old := newBoot
	newBoot = func(daemon.Home) bootManager { return f }
	t.Cleanup(func() { newBoot = old })
}

// fakeDetach replaces detach. It records the call and gives a pid.
func fakeDetach(t *testing.T, pid int) *[][]string {
	var calls [][]string
	old := detach
	detach = func(exe string, args []string, h daemon.Home, wait time.Duration) (int, error) {
		calls = append(calls, args)
		return pid, nil
	}
	t.Cleanup(func() { detach = old })
	return &calls
}

func runArgs(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func newHomeDir(t *testing.T) string { return filepath.Join(t.TempDir(), ".towertail") }

func TestStartNotSetUp(t *testing.T) {
	setBoot(t, &fakeBoot{})
	code, _, stderr := runArgs("start", "--home", newHomeDir(t))
	if code != 2 || !strings.Contains(stderr, "not set up: run start with --endpoint and --token-file") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestStartTokenFromStdin(t *testing.T) {
	setBoot(t, &fakeBoot{})
	calls := fakeDetach(t, 4242)
	setStdin(t, strings.NewReader("tt_abc\n"))
	dir := newHomeDir(t)
	code, stdout, stderr := runArgs("start", "--endpoint", "https://x.test", "--token-file", "-", "--boot", "none", "--home", dir)
	if code != 0 || stdout != "started, pid 4242\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	h := daemon.NewHome(dir)
	if tok, err := h.LoadToken(); err != nil || tok != "tt_abc" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	cfg, err := h.LoadConfig()
	if err != nil || cfg.Endpoint != "https://x.test" || cfg.Interval != 30*time.Second {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if b, _ := os.ReadFile(h.ConfigPath()); strings.Contains(string(b), "tt_abc") {
		t.Fatal("config holds the token")
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "start --foreground --home "+dir {
		t.Fatalf("detach args = %v", *calls)
	}
}

func TestStartEmptyTokenFails(t *testing.T) {
	setBoot(t, &fakeBoot{})
	setStdin(t, strings.NewReader("\n"))
	code, _, stderr := runArgs("start", "--endpoint", "https://x.test", "--token-file", "-", "--home", newHomeDir(t))
	if code != 2 || !strings.Contains(stderr, "empty token") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	setBoot(t, &fakeBoot{})
	calls := fakeDetach(t, 1)
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	_ = h.SaveConfig(daemon.StartConfig{Endpoint: "https://x.test", Boot: "none"})
	_ = h.SaveToken("tok")
	lock, err := h.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_ = h.WritePID(777)
	code, stdout, _ := runArgs("start", "--home", dir)
	if code != 0 || stdout != "already running, pid 777\n" {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	if len(*calls) != 0 {
		t.Fatal("started a second process")
	}
}

func TestStartBootEnabledOnce(t *testing.T) {
	fb := &fakeBoot{enableGot: "cron"}
	setBoot(t, fb)
	fakeDetach(t, 5)
	setStdin(t, strings.NewReader("tok\n"))
	dir := newHomeDir(t)
	if code, _, stderr := runArgs("start", "--endpoint", "https://x.test", "--token-file", "-", "--home", dir); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := runArgs("start", "--home", dir); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if len(fb.enabled) != 1 || fb.enabled[0] != "auto" {
		t.Fatalf("Enable calls = %v", fb.enabled)
	}
	if cfg, _ := daemon.NewHome(dir).LoadConfig(); cfg.Boot != "cron" {
		t.Fatalf("saved boot = %q", cfg.Boot)
	}
}

func TestStartBootFailureWarns(t *testing.T) {
	setBoot(t, &fakeBoot{enableErr: errors.New("no way")})
	fakeDetach(t, 5)
	setStdin(t, strings.NewReader("tok\n"))
	code, stdout, stderr := runArgs("start", "--endpoint", "https://x.test", "--token-file", "-", "--home", newHomeDir(t))
	if code != 0 || stdout != "started, pid 5\n" || !strings.Contains(stderr, "warning: reboot entry: no way") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestStartBootNoneDisablesEntry(t *testing.T) {
	fb := &fakeBoot{}
	setBoot(t, fb)
	fakeDetach(t, 5)
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	_ = h.SaveConfig(daemon.StartConfig{Endpoint: "https://x.test", Boot: "cron"})
	_ = h.SaveToken("tok")
	if code, _, stderr := runArgs("start", "--boot", "none", "--home", dir); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if fb.disabled != 1 || len(fb.enabled) != 0 {
		t.Fatalf("disabled = %d, enabled = %v", fb.disabled, fb.enabled)
	}
	if cfg, _ := h.LoadConfig(); cfg.Boot != "none" {
		t.Fatalf("saved boot = %q", cfg.Boot)
	}
}

func TestStartLingerUsesSystemd(t *testing.T) {
	fb := &fakeBoot{}
	setBoot(t, fb)
	calls := fakeDetach(t, 5)
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	_ = h.SaveConfig(daemon.StartConfig{Endpoint: "https://x.test", Boot: "linger"})
	_ = h.SaveToken("tok")
	// No process takes the lock, so the wait fails and the start falls back to detach.
	old := startWait
	startWait = 50 * time.Millisecond
	t.Cleanup(func() { startWait = old })
	code, stdout, stderr := runArgs("start", "--home", dir)
	if code != 0 || len(fb.serviceUsed) != 1 || fb.serviceUsed[0] != "start" || len(*calls) != 1 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q, service = %v", code, stdout, stderr, fb.serviceUsed)
	}
}

func TestStatusNotRunning(t *testing.T) {
	setBoot(t, &fakeBoot{detect: "none"})
	code, stdout, _ := runArgs("status", "--home", newHomeDir(t))
	if code != 0 || !strings.HasPrefix(stdout, `{"running":false,`) {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	if strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, `"reboot":"none"`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestStatusJSONFields(t *testing.T) {
	setBoot(t, &fakeBoot{detect: "linger"})
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	lock, err := h.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	_ = h.WritePID(321)
	sf := h.StateFile()
	_ = sf.Reset(321, "0.7.0+abc", "https://x.test")
	sf.PushResult(push.Result{At: time.Unix(1000, 0), Status: 202, Update: "0.7.1", Interval: 5 * time.Second})
	_, stdout, _ := runArgs("status", "--home", dir)
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	want := map[string]any{
		"running": true, "pid": 321.0, "version": "0.7.0+abc", "endpoint": "https://x.test",
		"last_status": 202.0, "update": "0.7.1", "interval": "5s", "reboot": "linger",
		"last_error": "", "gone": "",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	for _, k := range []string{"last_push", "paused_until"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no key %s", k)
		}
	}
	if m["last_push"] == nil {
		t.Error("last_push is null")
	}
}

func TestStopNotRunning(t *testing.T) {
	setBoot(t, &fakeBoot{})
	code, stdout, _ := runArgs("stop", "--home", newHomeDir(t))
	if code != 0 || stdout != "not running\n" {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

func TestBootEnableShowDisable(t *testing.T) {
	fb := &fakeBoot{enableGot: "cron", detect: "cron"}
	setBoot(t, fb)
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	_ = h.SaveConfig(daemon.StartConfig{Endpoint: "https://x.test", Boot: "none"})

	code, stdout, _ := runArgs("boot", "enable", "--mode", "cron", "--home", dir)
	if code != 0 || stdout != `{"reboot":"cron"}`+"\n" {
		t.Fatalf("enable: code = %d, stdout = %q", code, stdout)
	}
	if len(fb.enabled) != 1 || fb.enabled[0] != "cron" {
		t.Fatalf("Enable calls = %v", fb.enabled)
	}
	if cfg, _ := h.LoadConfig(); cfg.Boot != "cron" {
		t.Fatalf("saved boot = %q", cfg.Boot)
	}

	if code, stdout, _ = runArgs("boot", "show", "--home", dir); code != 0 || stdout != `{"reboot":"cron"}`+"\n" {
		t.Fatalf("show: code = %d, stdout = %q", code, stdout)
	}

	code, stdout, _ = runArgs("boot", "disable", "--home", dir)
	if code != 0 || stdout != `{"reboot":"none"}`+"\n" || fb.disabled != 1 {
		t.Fatalf("disable: code = %d, stdout = %q", code, stdout)
	}
	if cfg, _ := h.LoadConfig(); cfg.Boot != "none" {
		t.Fatalf("saved boot = %q", cfg.Boot)
	}
}

func TestBootEnableErrorLine(t *testing.T) {
	setBoot(t, &fakeBoot{enableErr: errors.New("no way")})
	code, stdout, _ := runArgs("boot", "enable", "--home", newHomeDir(t))
	if code != 1 || stdout != `{"error":"no way"}`+"\n" {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

func TestRunPushGoneDisablesBootAndForgets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		io.WriteString(w, `{"action":"exit","reason":"host_removed"}`)
	}))
	defer srv.Close()
	h := daemon.NewHome(newHomeDir(t))
	_ = h.SaveConfig(daemon.StartConfig{Endpoint: srv.URL})
	_ = h.SaveToken("tok")
	fb := &fakeBoot{}
	var log bytes.Buffer
	cfg := push.Config{
		Endpoint: srv.URL, Token: "tok", Interval: time.Hour,
		FlushInterval: 10 * time.Millisecond, Recorder: h.StateFile(),
	}
	done := make(chan error, 1)
	go func() {
		done <- runPush(context.Background(), cfg, func() schema.Sample { return schema.Sample{V: schema.SchemaVersion} }, h, fb, &log)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runPush = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runPush did not return")
	}
	if fb.disabled != 1 {
		t.Errorf("Disable calls = %d", fb.disabled)
	}
	for _, p := range []string{h.ConfigPath(), h.TokenPath()} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s stays", filepath.Base(p))
		}
	}
	if st, _ := h.StateFile().Read(); st.Gone != "host_removed" {
		t.Errorf("state = %+v", st)
	}
	if !strings.Contains(log.String(), "cloud asked to exit: host_removed") {
		t.Errorf("log = %q", log.String())
	}
}

// TestStartDetachReal starts the test binary as the background sampler.
func TestStartDetachReal(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a process")
	}
	t.Setenv(asSampler, "1")
	setBoot(t, &fakeBoot{detect: "none"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	dir := newHomeDir(t)
	h := daemon.NewHome(dir)
	t.Cleanup(func() { _, _ = h.Terminate(5 * time.Second) })

	setStdin(t, strings.NewReader("tok\n"))
	code, stdout, stderr := runArgs("start", "--endpoint", srv.URL, "--token-file", "-", "--boot", "none", "--home", dir)
	if code != 0 || !strings.HasPrefix(stdout, "started, pid ") {
		t.Fatalf("start: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	_, stdout, _ = runArgs("status", "--home", dir)
	var st daemon.Status
	if err := json.Unmarshal([]byte(stdout), &st); err != nil || !st.Running || st.PID == 0 {
		t.Fatalf("status = %q, %v", stdout, err)
	}

	code, stdout, _ = runArgs("start", "--home", dir)
	if code != 0 || !strings.HasPrefix(stdout, "already running, pid ") {
		t.Fatalf("second start: code = %d, stdout = %q", code, stdout)
	}

	code, stdout, stderr = runArgs("stop", "--home", dir)
	if code != 0 || stdout != "stopped\n" {
		t.Fatalf("stop: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if pid, ok := h.Running(); ok {
		t.Fatalf("still running, pid %d", pid)
	}
	if _, err := os.Stat(h.PIDPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("pid file stays")
	}
	if b, _ := os.ReadFile(h.LogPath()); !strings.Contains(string(b), "start: sampler") {
		t.Errorf("log = %q", b)
	}
}
