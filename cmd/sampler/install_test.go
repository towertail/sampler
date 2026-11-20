//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runInstall runs install.sh with a fake uname.
func runInstall(t *testing.T, sys, machine string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\ncase \"$1\" in\n-s) echo " + sys + " ;;\n-m) echo " + machine + " ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "uname"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallScriptTriple(t *testing.T) {
	for _, tc := range []struct{ sys, machine, want string }{
		{"Linux", "x86_64", "linux-amd64"},
		{"Linux", "aarch64", "linux-arm64"},
		{"Linux", "armv7l", "linux-armv7"},
		{"Darwin", "arm64", "darwin-arm64"},
		{"Darwin", "x86_64", "darwin-amd64"},
	} {
		out, err := runInstall(t, tc.sys, tc.machine, "--join", "j", "--dry-run")
		if err != nil {
			t.Fatalf("%s %s: %v: %s", tc.sys, tc.machine, err, out)
		}
		base := "https://github.com/towertail/sampler/releases/latest/download"
		want := "triple=" + tc.want + "\nurl=" + base + "/towertail-sampler-" + tc.want + "\nmanifest=" + base + "/manifest.json\n"
		if out != want {
			t.Errorf("%s %s: output\n%s\nwant\n%s", tc.sys, tc.machine, out, want)
		}
	}
}

func TestInstallScriptVersion(t *testing.T) {
	for _, v := range []string{"0.8.0", "v0.8.0"} {
		out, err := runInstall(t, "Linux", "x86_64", "--join", "j", "--version", v, "--dry-run")
		if err != nil || !strings.Contains(out, "url=https://github.com/towertail/sampler/releases/download/v0.8.0/towertail-sampler-linux-amd64\n") {
			t.Errorf("--version %s: %v: %s", v, err, out)
		}
	}
}

func TestInstallScriptErrors(t *testing.T) {
	for _, tc := range []struct {
		name, sys, machine, want string
		args                     []string
	}{
		{"no join", "Linux", "x86_64", "--join <token> is required", []string{"--dry-run"}},
		{"windows", "MINGW64_NT", "x86_64", "unsupported OS", []string{"--join", "j", "--dry-run"}},
		{"cpu", "Linux", "mips", "unsupported CPU", []string{"--join", "j", "--dry-run"}},
		{"mac arm32", "Darwin", "armv7l", "unsupported CPU", []string{"--join", "j", "--dry-run"}},
		{"missing value", "Linux", "x86_64", "--name needs a value", []string{"--join", "j", "--name"}},
		{"unknown flag", "Linux", "x86_64", "unknown argument", []string{"--x"}},
	} {
		out, err := runInstall(t, tc.sys, tc.machine, tc.args...)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: err = %v, output = %q, want %q", tc.name, err, out, tc.want)
		}
	}
}
