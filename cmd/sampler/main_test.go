package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/towertail/sampler/internal/version"
	"github.com/towertail/sampler/pkg/schema"
)

func TestRunVersion(t *testing.T) {
	oldV, oldSHA := version.Version, version.SHA
	t.Cleanup(func() { version.Version, version.SHA = oldV, oldSHA })
	version.Version, version.SHA = "1.2.3", "abc1234"

	const want = "towertail-sampler 1.2.3+abc1234\n"
	for _, arg := range []string{"--version", "-V", "version"} {
		t.Run(arg, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{arg}, &out, &errb); code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, errb.String())
			}
			if out.String() != want {
				t.Errorf("got %q, want %q", out.String(), want)
			}
		})
	}
}

func TestRunSelfCheck(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--self-check"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Errorf("expected 'ok', got %q", out.String())
	}
}

func TestRunOnce(t *testing.T) {
	baseKeys := []string{"ts", "host", "cpu", "mem", "swap", "errors"}
	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, m map[string]any)
	}{
		{"default", nil, func(t *testing.T, m map[string]any) {
			if v, ok := m["v"].(float64); !ok || int(v) != 1 {
				t.Errorf("v != 1: %v", m["v"])
			}
			for _, k := range baseKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("missing key %q", k)
				}
			}
		}},
		{"once", []string{"--once"}, func(t *testing.T, m map[string]any) {
			for _, k := range baseKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("missing key %q", k)
				}
			}
		}},
		{"no-disk-no-net", []string{"--once", "--no-disk", "--no-net"}, func(t *testing.T, m map[string]any) {
			for _, k := range []string{"disks", "net"} {
				if _, ok := m[k]; ok {
					t.Errorf("%q should be omitted", k)
				}
			}
		}},
		{"top-n", []string{"--once", "--top-n", "5"}, func(t *testing.T, m map[string]any) {
			procs, ok := m["procs"].(map[string]any)
			if !ok {
				t.Fatalf("expected procs object, got: %v", m["procs"])
			}
			if procs["top_n"] != float64(5) {
				t.Errorf("top_n: got %v want 5", procs["top_n"])
			}
			items, _ := procs["items"].([]any)
			// Top 5 by CPU, top 5 by RSS and top 10 by I/O.
			if len(items) == 0 || len(items) > 20 {
				t.Errorf("want 1 to 20 items, got %d", len(items))
			}
		}},
		{"no-proc", []string{"--once", "--no-proc"}, func(t *testing.T, m map[string]any) {
			if _, ok := m["procs"]; ok {
				t.Error("procs should be omitted")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			if code := run(tt.args, &out, &errb); code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, errb.String())
			}
			var m map[string]any
			if err := json.Unmarshal(out.Bytes(), &m); err != nil {
				t.Fatalf("invalid json: %v\n%s", err, out.String())
			}
			tt.check(t, m)
		})
	}
}

func TestRunOnceNoProcCwdOmitsFolders(t *testing.T) {
	folders := func(args ...string) (withCwd, withRepo int) {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 0 {
			t.Fatalf("exit = %d, stderr=%s", code, errb.String())
		}
		var s schema.Sample
		if err := json.Unmarshal(out.Bytes(), &s); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		for _, it := range s.Procs.Items {
			if it.Cwd != "" {
				withCwd++
			}
			if it.Repo != "" {
				withRepo++
			}
		}
		return withCwd, withRepo
	}
	if cwd, repo := folders("--once", "--no-ports", "--no-proc-cwd"); cwd != 0 || repo != 0 {
		t.Errorf("--no-proc-cwd: %d cwd, %d repo, want none", cwd, repo)
	}
	if cwd, repo := folders("once", "--no-ports", "--no-proc-cwd"); cwd != 0 || repo != 0 {
		t.Errorf("once --no-proc-cwd: %d cwd, %d repo, want none", cwd, repo)
	}
	if cwd, _ := folders("--once", "--no-ports"); cwd == 0 {
		t.Skip("no process with a readable working folder")
	}
}

func TestResolveTokenStdin(t *testing.T) {
	resolve := func(in string, args ...string) (string, error) {
		var tok string
		var err error
		cmd := &cli.Command{
			Name:  "x",
			Flags: []cli.Flag{&cli.StringFlag{Name: "token"}, &cli.StringFlag{Name: "token-file"}},
			Action: func(ctx context.Context, c *cli.Command) error {
				tok, err = resolveToken(c, strings.NewReader(in))
				return nil
			},
		}
		if rerr := cmd.Run(context.Background(), append([]string{"x"}, args...)); rerr != nil {
			t.Fatal(rerr)
		}
		return tok, err
	}
	if tok, err := resolve("tt_abc\nmore\n", "--token-file", "-"); err != nil || tok != "tt_abc" {
		t.Errorf("stdin token = %q, %v", tok, err)
	}
	if _, err := resolve("\n", "--token-file", "-"); err == nil {
		t.Error("empty line: want an error")
	}
	if _, err := resolve("", "--token-file", "-"); err == nil {
		t.Error("no input: want an error")
	}
	if tok, err := resolve("", "--token", "tt_x"); err != nil || tok != "tt_x" {
		t.Errorf("flag token = %q, %v", tok, err)
	}
	if _, err := resolve(""); err == nil {
		t.Error("no token: want an error")
	}
	path := t.TempDir() + "/tok"
	_ = os.WriteFile(path, []byte("tt_file\n"), 0o600)
	if tok, err := resolve("", "--token-file", path); err != nil || tok != "tt_file" {
		t.Errorf("file token = %q, %v", tok, err)
	}
}
