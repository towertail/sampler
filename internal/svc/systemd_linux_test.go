//go:build linux

package svc

import (
	"strings"
	"testing"
	"time"
)

func TestRenderUnit(t *testing.T) {
	cases := []struct {
		name        string
		system      bool
		execPath    string
		tokenFile   string
		protectHome string
		execStart   string
	}{
		{
			name:        "user",
			execPath:    "/home/ops/.towertail/towertail-sampler",
			tokenFile:   "/home/ops/.config/towertail/sampler.token",
			protectHome: "read-only",
			execStart:   "ExecStart=/home/ops/.towertail/towertail-sampler push --endpoint https://tt.example/x --token-file /home/ops/.config/towertail/sampler.token --interval 30s\n",
		},
		{
			name:        "system",
			system:      true,
			execPath:    "/usr/local/bin/towertail-sampler",
			tokenFile:   "/etc/towertail/sampler.token",
			protectHome: "yes",
			execStart:   "ExecStart=/usr/local/bin/towertail-sampler push --endpoint https://tt.example/x --token-file /etc/towertail/sampler.token --interval 30s\n",
		},
		{
			name:        "system binary in home",
			system:      true,
			execPath:    "/root/bin/towertail-sampler",
			tokenFile:   "/etc/towertail/sampler.token",
			protectHome: "read-only",
			execStart:   "ExecStart=/root/bin/towertail-sampler push",
		},
		{
			name:        "path with space",
			execPath:    "/home/ops/my bin/towertail-sampler",
			tokenFile:   "/home/ops/.config/towertail/sampler.token",
			protectHome: "read-only",
			execStart:   `ExecStart="/home/ops/my bin/towertail-sampler" push`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &systemd{system: tc.system}
			b, err := s.renderUnit(InstallConfig{
				ExecPath: tc.execPath,
				Endpoint: "https://tt.example/x",
				Interval: 30 * time.Second,
			}, tc.tokenFile)
			if err != nil {
				t.Fatal(err)
			}
			unit := string(b)
			if !strings.Contains(unit, "\nProtectHome="+tc.protectHome+"\n") {
				t.Errorf("want ProtectHome=%s:\n%s", tc.protectHome, unit)
			}
			if !strings.Contains(unit, tc.execStart) {
				t.Errorf("want %q:\n%s", tc.execStart, unit)
			}
		})
	}
}

func TestUnitArgEscapes(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/x":      "/usr/bin/x",
		"https://h/a%20b": "https://h/a%%20b",
		`/a b/"c"`:        `"/a b/\"c\""`,
		"/opt/$HOME/x y":  `"/opt/$$HOME/x y"`,
		`C:\x y`:          `"C:\\x y"`,
		"/a/b;rm":         `"/a/b;rm"`,
	}
	for in, want := range cases {
		if got := unitArg(in); got != want {
			t.Errorf("unitArg(%q) = %q, want %q", in, got, want)
		}
	}
}
