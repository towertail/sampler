//go:build darwin

package svc

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPlistAllowFlags(t *testing.T) {
	cfg := InstallConfig{ExecPath: "/usr/local/bin/towertail-sampler", Endpoint: "https://tt.example", Interval: 30 * time.Second}
	b, err := renderPlist(cfg, "/t", "/l")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--allow-") {
		t.Errorf("plist has an allow flag:\n%s", b)
	}

	cfg.AllowControl, cfg.AllowDiskScan = true, true
	if b, err = renderPlist(cfg, "/t", "/l"); err != nil {
		t.Fatal(err)
	}
	want := "<string>30s</string>\n      <string>--allow-control</string>\n      <string>--allow-disk-scan</string>\n    </array>"
	if !strings.Contains(string(b), want) {
		t.Errorf("plist:\n%s", b)
	}
}
