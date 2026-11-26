package main

import (
	"fmt"
	"io"
	"sync/atomic"

	"github.com/towertail/sampler/internal/collect"
	"github.com/towertail/sampler/internal/push"
	"github.com/towertail/sampler/pkg/schema"
)

// procCwdSwitch holds the folder setting of a push loop. The server can
// change it in an ingest reply.
type procCwdSwitch struct {
	off  atomic.Bool
	save func(off bool) error
	log  io.Writer
}

// newProcCwdSwitch returns a switch. A nil save keeps the change in memory.
func newProcCwdSwitch(off bool, save func(off bool) error, log io.Writer) *procCwdSwitch {
	s := &procCwdSwitch{save: save, log: log}
	s.off.Store(off)
	return s
}

// Apply is push.Config.OnProcCwd. It saves a changed value.
func (s *procCwdSwitch) Apply(send bool) {
	if s.off.Swap(!send) == !send {
		return
	}
	fmt.Fprintf(s.log, "server set proc_cwd: %t\n", send)
	if s.save == nil {
		return
	}
	if err := s.save(!send); err != nil {
		fmt.Fprintf(s.log, "save config: %v\n", err)
	}
}

// sampleFn returns the sample function of the push loop. Each sample uses
// the current value of the switch.
func (s *procCwdSwitch) sampleFn(opts options, pc *portCache) push.SampleFn {
	return func() schema.Sample {
		o := opts
		o.noProcCwd = s.off.Load()
		return buildSample(&o, collect.SampleWindow, pc)
	}
}
