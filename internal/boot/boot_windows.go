package boot

import "context"

func (e *Entry) Enable(ctx context.Context, mode string) (string, error) { return "", ErrUnsupported }
func (e *Entry) Disable(ctx context.Context) error                       { return ErrUnsupported }
func (e *Entry) Detect(ctx context.Context) string                       { return "none" }
func (e *Entry) StartService(ctx context.Context) error                  { return ErrUnsupported }
func (e *Entry) StopService(ctx context.Context) error                   { return ErrUnsupported }
