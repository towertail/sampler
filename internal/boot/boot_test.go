//go:build !windows

package boot

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/towertail/sampler/internal/daemon"
)

// codeErr is a command failure with an exit code.
type codeErr int

func (c codeErr) Error() string { return "exit " + strconv.Itoa(int(c)) }
func (c codeErr) ExitCode() int { return int(c) }

// fakeRunner records the calls. A call fails when it starts with a key
// of fail. It keeps a crontab in memory.
type fakeRunner struct {
	calls   []string
	fail    map[string]error
	out     map[string]string
	crontab string
	hasCron bool
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	for prefix, err := range f.fail {
		if strings.HasPrefix(call, prefix) {
			return nil, err
		}
	}
	for prefix, out := range f.out {
		if strings.HasPrefix(call, prefix) {
			return []byte(out), nil
		}
	}
	switch call {
	case "crontab -l":
		if !f.hasCron {
			return nil, errors.New("crontab: exit status 1: no crontab for u")
		}
		return []byte(f.crontab), nil
	case "crontab -":
		b, _ := io.ReadAll(stdin)
		f.crontab, f.hasCron = string(b), true
	case "crontab -r":
		f.crontab, f.hasCron = "", false
	}
	return nil, nil
}

func (f *fakeRunner) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newTestEntry(t *testing.T, goos string, f *fakeRunner) *Entry {
	t.Helper()
	dir := t.TempDir()
	return &Entry{
		home:     daemon.NewHome(filepath.Join(dir, ".towertail")),
		run:      f,
		user:     "u",
		uid:      501,
		goos:     goos,
		unitDir:  filepath.Join(dir, "units"),
		agentDir: filepath.Join(dir, "agents"),
	}
}

func markedLines(s string) int { return strings.Count(s, marker) }

func TestRenderUnitStartForeground(t *testing.T) {
	u := renderUnit("/h/.towertail/bin/towertail-sampler")
	for _, want := range []string{
		"ExecStart=/h/.towertail/bin/towertail-sampler start --foreground\n",
		"Restart=on-failure\n",
		"Type=simple\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit has no %q:\n%s", want, u)
		}
	}
}

func TestRenderPlistStartForeground(t *testing.T) {
	p := renderPlist("/h/bin/towertail-sampler")
	for _, want := range []string{
		"<string>/h/bin/towertail-sampler</string>\n\t\t<string>start</string>\n\t\t<string>--foreground</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>SuccessfulExit</key>\n\t\t<false/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist has no %q:\n%s", want, p)
		}
	}
	if runtime.GOOS != "darwin" {
		return
	}
	path := filepath.Join(t.TempDir(), "a.plist")
	if err := os.WriteFile(path, []byte(p), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v: %s", err, out)
	}
}

func TestEnableLingerOK(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "linux", f)
	got, err := e.Enable(context.Background(), "auto")
	if err != nil || got != "linger" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	want := []string{
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable towertail-sampler.service",
		"loginctl enable-linger u",
	}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q", f.calls)
	}
	b, err := os.ReadFile(e.unitPath())
	if err != nil || !strings.Contains(string(b), e.home.Bin()+" start --foreground") {
		t.Fatalf("unit = %q, %v", b, err)
	}
}

func TestEnableLingerRefusedFallsBackToCron(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{"loginctl enable-linger": errors.New("refused")}}
	e := newTestEntry(t, "linux", f)
	got, err := e.Enable(context.Background(), "auto")
	if err != nil || got != "cron" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	if _, err := os.Stat(e.unitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit file stays: %v", err)
	}
	if n := markedLines(f.crontab); n != 2 {
		t.Errorf("marked cron lines = %d:\n%s", n, f.crontab)
	}
}

func TestEnableLingerOnlyDoesNotFallBack(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{"loginctl enable-linger": errors.New("refused")}}
	e := newTestEntry(t, "linux", f)
	if got, err := e.Enable(context.Background(), "linger"); err == nil {
		t.Fatalf("Enable = %q, want an error", got)
	}
	if f.called("crontab") {
		t.Error("cron was used")
	}
}

func TestEnableNoSystemd(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{"systemctl --user show-environment": errors.New("no bus")}}
	e := newTestEntry(t, "linux", f)
	got, err := e.Enable(context.Background(), "auto")
	if err != nil || got != "cron" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	if _, err := os.Stat(e.unitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("unit file written without systemd")
	}
}

func TestEnableNothingWorks(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{
		"systemctl": errors.New("no bus"),
		"crontab":   exec.ErrNotFound,
	}}
	e := newTestEntry(t, "linux", f)
	if _, err := e.Enable(context.Background(), "auto"); !errors.Is(err, ErrNoBootMethod) {
		t.Fatalf("err = %v, want ErrNoBootMethod", err)
	}
}

func TestEnableCronDoesNotClobberOnReadError(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{"crontab -l": errors.New("crontab: permission denied")}}
	e := newTestEntry(t, "linux", f)
	if _, err := e.Enable(context.Background(), "cron"); err == nil {
		t.Fatal("want an error")
	}
	for _, c := range f.calls {
		if c == "crontab -" {
			t.Fatal("crontab written after a read error")
		}
	}
}

func TestEditCrontabKeepsOtherLines(t *testing.T) {
	const bin = "/h/bin/towertail-sampler"
	old := "MAILTO=me\n0 3 * * * backup\n"
	once := editCrontab(old, bin, true)
	if !strings.HasPrefix(once, old) || markedLines(once) != 2 {
		t.Fatalf("crontab:\n%s", once)
	}
	if !strings.Contains(once, "@reboot "+bin+" start ") || !strings.Contains(once, "*/5 * * * * "+bin+" start ") {
		t.Fatalf("crontab:\n%s", once)
	}
	if twice := editCrontab(once, bin, true); twice != once {
		t.Fatalf("second enable changed the crontab:\n%s", twice)
	}
	if got := editCrontab(once, bin, false); got != old {
		t.Fatalf("remove gave:\n%s", got)
	}
	if got := editCrontab("", bin, false); got != "" {
		t.Fatalf("empty crontab gave %q", got)
	}
}

func TestDisableRemovesBoth(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "linux", f)
	f.crontab, f.hasCron = "0 3 * * * backup\n", true
	if _, err := e.Enable(context.Background(), "linger"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Enable(context.Background(), "cron"); err != nil {
		t.Fatal(err)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.unitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("unit file stays")
	}
	if f.crontab != "0 3 * * * backup\n" {
		t.Errorf("crontab = %q", f.crontab)
	}
	if !f.called("systemctl --user disable towertail-sampler.service") {
		t.Error("unit was not disabled")
	}
}

func TestDisableDropsEmptyCrontab(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "linux", f)
	if _, err := e.Enable(context.Background(), "cron"); err != nil {
		t.Fatal(err)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.hasCron {
		t.Errorf("crontab stays: %q", f.crontab)
	}
}

func TestDisableNothingToRemove(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "linux", f)
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c != "crontab -l" {
			t.Errorf("unexpected call %q", c)
		}
	}
}

func TestDetect(t *testing.T) {
	ctx := context.Background()
	cron := "@reboot x start " + marker + "\n"
	write := func(e *Entry, path string) {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		goos  string
		setup func(e *Entry, f *fakeRunner)
		want  string
	}{
		{"none", "linux", func(e *Entry, f *fakeRunner) {}, "none"},
		{"cron", "linux", func(e *Entry, f *fakeRunner) { f.crontab, f.hasCron = cron, true }, "cron"},
		{"linger", "linux", func(e *Entry, f *fakeRunner) {
			write(e, e.unitPath())
			f.out = map[string]string{"loginctl show-user": "Linger=yes\n"}
		}, "linger"},
		{"unit without linger", "linux", func(e *Entry, f *fakeRunner) {
			write(e, e.unitPath())
			f.out = map[string]string{"loginctl show-user": "Linger=no\n"}
		}, "none"},
		{"launchagent", "darwin", func(e *Entry, f *fakeRunner) { write(e, e.plistPath()) }, "launchagent"},
		{"launchagent+cron", "darwin", func(e *Entry, f *fakeRunner) {
			write(e, e.plistPath())
			f.crontab, f.hasCron = cron, true
		}, "launchagent+cron"},
		{"plist not loaded", "darwin", func(e *Entry, f *fakeRunner) {
			write(e, e.plistPath())
			f.fail = map[string]error{"launchctl print": codeErr(113)}
		}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			e := newTestEntry(t, tc.goos, f)
			tc.setup(e, f)
			if got := e.Detect(ctx); got != tc.want {
				t.Errorf("Detect = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnableDarwinLaunchAgent(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "darwin", f)
	got, err := e.Enable(context.Background(), "auto")
	if err != nil || got != "launchagent" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	if !f.called("launchctl bootstrap gui/501 " + e.plistPath()) {
		t.Errorf("calls = %q", f.calls)
	}
	if f.called("crontab") {
		t.Error("cron was used with auto-login")
	}
}

func TestEnableDarwinNoGUISession(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{
		"launchctl bootstrap gui/501": codeErr(125),
		"defaults read":               errors.New("no autoLoginUser"),
	}}
	e := newTestEntry(t, "darwin", f)
	got, err := e.Enable(context.Background(), "auto")
	if err != nil || got != "launchagent+cron" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	if !f.called("launchctl bootstrap user/501 " + e.plistPath()) {
		t.Errorf("calls = %q", f.calls)
	}
	if markedLines(f.crontab) != 2 {
		t.Errorf("crontab:\n%s", f.crontab)
	}
}

func TestEnableDarwinAlreadyLoaded(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{"launchctl bootstrap": codeErr(5)}}
	e := newTestEntry(t, "darwin", f)
	if got, err := e.Enable(context.Background(), "launchagent"); err != nil || got != "launchagent" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
}

func TestEnableDarwinBootstrapFails(t *testing.T) {
	f := &fakeRunner{fail: map[string]error{
		"launchctl": codeErr(1),
		"crontab":   exec.ErrNotFound,
	}}
	e := newTestEntry(t, "darwin", f)
	if _, err := e.Enable(context.Background(), "auto"); !errors.Is(err, ErrNoBootMethod) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(e.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("plist stays after a failed bootstrap")
	}
}

func TestEnableLaunchAgentOnLinuxFails(t *testing.T) {
	e := newTestEntry(t, "linux", &fakeRunner{})
	if _, err := e.Enable(context.Background(), "launchagent"); err == nil {
		t.Fatal("want an error")
	}
}

func TestDisableRemovesPlist(t *testing.T) {
	f := &fakeRunner{}
	e := newTestEntry(t, "darwin", f)
	if got, err := e.Enable(context.Background(), "auto"); err != nil || got != "launchagent+cron" && got != "launchagent" {
		t.Fatalf("Enable = %q, %v", got, err)
	}
	if _, err := e.Enable(context.Background(), "cron"); err != nil {
		t.Fatal(err)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Error("plist stays")
	}
	if f.hasCron {
		t.Errorf("crontab stays: %q", f.crontab)
	}
	if f.called("launchctl bootout") {
		t.Error("Disable ran bootout")
	}
}

func TestEnableUnknownMode(t *testing.T) {
	e := newTestEntry(t, "linux", &fakeRunner{})
	if _, err := e.Enable(context.Background(), "x"); err == nil {
		t.Fatal("want an error")
	}
}
