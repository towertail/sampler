// Package svc manages the sampler as a background service on macOS
// (launchd) and Linux (systemd). The package shells out to platform
// tools and writes unit/plist templates ourselves — no dependency on
// kardianos/service.
package svc

import (
	"context"
	"errors"
	"io"
	"time"
)

// Manager is the platform-specific service manager.
type Manager interface {
	Install(ctx context.Context, cfg InstallConfig) error
	Uninstall(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Status(ctx context.Context) (string, error)
	Log(ctx context.Context, follow bool, w io.Writer) error
}

// Options controls which manager flavor to build.
type Options struct {
	// System=true installs a system-wide service. Defaults to a
	// per-user service, which is what most operators want.
	System bool
}

// InstallConfig captures what the sampler needs to know at install
// time to generate the unit/plist.
type InstallConfig struct {
	ExecPath     string
	Endpoint     string
	Token        string
	User         string
	Interval     time.Duration
	AllowControl bool
}

// ErrUnsupported is returned by NewManager when the host OS does not
// have a supported service manager implementation.
var ErrUnsupported = errors.New("svc: unsupported OS")

// NewManager returns the correct manager for the current OS. The
// build-tag-scoped files define platformNewManager — on unsupported
// OSes the fallback returns ErrUnsupported.
func NewManager(opts Options) (Manager, error) {
	return platformNewManager(opts)
}
