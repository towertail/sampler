//go:build !windows

package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/towertail/sampler/internal/push"
)

func newHome(t *testing.T) Home {
	t.Helper()
	return NewHome(filepath.Join(t.TempDir(), ".towertail"))
}

func TestLockSecondLockFails(t *testing.T) {
	h := newHome(t)
	l, err := h.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Lock(); !errors.Is(err, ErrRunning) {
		t.Fatalf("second lock: %v, want ErrRunning", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := h.Lock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = l2.Release()
}

func TestRunningStalePID(t *testing.T) {
	h := newHome(t)
	if err := h.WritePID(99999); err != nil {
		t.Fatal(err)
	}
	if pid, ok := h.Running(); ok {
		t.Fatalf("running with pid %d, want a free lock", pid)
	}
	l, err := h.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if pid, ok := h.Running(); !ok || pid != 99999 {
		t.Fatalf("Running = %d, %v", pid, ok)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	h := newHome(t)
	if _, err := h.LoadConfig(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty home: %v", err)
	}
	want := StartConfig{Endpoint: "https://x", Interval: 30 * time.Second, AllowControl: true, Insecure: true, Boot: "cron"}
	if err := h.SaveConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := h.LoadConfig()
	if err != nil || got != want {
		t.Fatalf("config = %+v, %v", got, err)
	}
	assertMode(t, h.ConfigPath(), 0o600)
}

func TestTokenMode0600(t *testing.T) {
	h := newHome(t)
	if err := h.SaveToken("tt_abc"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, h.TokenPath(), 0o600)
	if tok, err := h.LoadToken(); err != nil || tok != "tt_abc" {
		t.Fatalf("token = %q, %v", tok, err)
	}
}

func TestForgetKeepsLogAndState(t *testing.T) {
	h := newHome(t)
	_ = h.SaveConfig(StartConfig{Endpoint: "x"})
	_ = h.SaveToken("t")
	_ = h.WritePID(1234)
	_ = os.WriteFile(h.LogPath(), []byte("log"), 0o600)
	_ = os.WriteFile(h.StatePath(), []byte("{}"), 0o600)
	if err := h.Forget(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{h.ConfigPath(), h.TokenPath(), h.PIDPath()} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists", filepath.Base(p))
		}
	}
	for _, p := range []string{h.LogPath(), h.StatePath()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is gone", filepath.Base(p))
		}
	}
}

func TestRotatingFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sampler.log")
	r, err := OpenRotating(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	first, second := make([]byte, 75), make([]byte, 75)
	for i := range first {
		first[i], second[i] = 'a', 'b'
	}
	_, _ = r.Write(first)
	_, _ = r.Write(second)
	if b, _ := os.ReadFile(path + ".1"); string(b) != string(first) {
		t.Errorf(".1 = %q", b)
	}
	if b, _ := os.ReadFile(path); string(b) != string(second) {
		t.Errorf("log = %q", b)
	}
}

func TestStateFileAtomicMerge(t *testing.T) {
	h := newHome(t)
	s := h.StateFile()
	t1, t2 := time.Unix(1000, 0), time.Unix(2000, 0)
	s.PushResult(push.Result{At: t1, Status: 202, Update: "0.7.1", Interval: 5 * time.Second})
	s.PushResult(push.Result{At: t2, Status: 202, Interval: 30 * time.Second})
	st, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if st.LastPush == nil || !st.LastPush.Equal(t2) {
		t.Errorf("last_push = %v, want %v", st.LastPush, t2)
	}
	if st.Update != "0.7.1" || st.Interval != "30s" || st.LastStatus != 202 {
		t.Errorf("state = %+v", st)
	}
	assertMode(t, h.StatePath(), 0o600)
	if m, _ := filepath.Glob(filepath.Join(h.Dir, ".tmp-*")); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}
}

func TestStateFilePauseAndGone(t *testing.T) {
	s := newHome(t).StateFile()
	until := time.Unix(5000, 0)
	s.PushResult(push.Result{At: time.Unix(1, 0), Status: 402, Err: "plan full", PausedUntil: until})
	st, _ := s.Read()
	if st.PausedUntil == nil || !st.PausedUntil.Equal(until) || st.LastPush != nil || st.LastError != "plan full" {
		t.Fatalf("after 402: %+v", st)
	}
	s.PushResult(push.Result{At: time.Unix(9000, 0), Status: 202})
	st, _ = s.Read()
	if st.PausedUntil != nil || st.LastError != "" {
		t.Fatalf("after 202: %+v", st)
	}
	s.PushResult(push.Result{Status: 410, Gone: "host_removed"})
	if st, _ = s.Read(); st.Gone != "host_removed" {
		t.Fatalf("after 410: %+v", st)
	}
	if err := s.Reset(7, "1.0.0+x", "https://e"); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.Read(); st.Gone != "" || st.PID != 7 || st.Endpoint != "https://e" {
		t.Fatalf("after reset: %+v", st)
	}
}

func TestSetBoot(t *testing.T) {
	h := newHome(t)
	if err := h.SetBoot("cron"); err != nil {
		t.Fatalf("no config: %v", err)
	}
	_ = h.SaveConfig(StartConfig{Endpoint: "x"})
	if err := h.SetBoot("cron"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.LoadConfig(); c.Boot != "cron" || c.Endpoint != "x" {
		t.Fatalf("config = %+v", c)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func TestStampPrefixesTime(t *testing.T) {
	var b bytes.Buffer
	n, err := Stamp(&b).Write([]byte("hello\n"))
	if err != nil || n != 6 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	ts, rest, ok := strings.Cut(b.String(), " ")
	if _, perr := time.Parse(time.RFC3339, ts); !ok || perr != nil || rest != "hello\n" {
		t.Fatalf("line = %q", b.String())
	}
}
