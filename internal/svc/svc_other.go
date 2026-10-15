//go:build !darwin && !linux

package svc

import (
	"context"
	"io"
)

type stub struct{}

func platformNewManager(Options) (Manager, error) { return nil, ErrUnsupported }

// The fallback platform also provides a concrete Manager so future
// callers (tests, docs) can construct one without build-tagging their
// own code. It returns ErrUnsupported for every method.
func NewStubManager() Manager { return &stub{} }

func (s *stub) Install(context.Context, InstallConfig) error { return ErrUnsupported }
func (s *stub) Uninstall(context.Context) error              { return ErrUnsupported }
func (s *stub) Start(context.Context) error                  { return ErrUnsupported }
func (s *stub) Stop(context.Context) error                   { return ErrUnsupported }
func (s *stub) Status(context.Context) (string, error)       { return "", ErrUnsupported }
func (s *stub) Log(context.Context, bool, io.Writer) error   { return ErrUnsupported }
