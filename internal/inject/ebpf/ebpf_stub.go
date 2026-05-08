//go:build !linux

package ebpf

import (
	"context"
	"errors"

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

// ErrUnsupported is returned by Start when faultkit is built for a
// non-Linux platform. eBPF mode is Linux-only.
var ErrUnsupported = errors.New("ebpf: not supported on this platform")

// Injector is a non-Linux placeholder that fails Start with
// ErrUnsupported.
type Injector struct {
	events chan inject.Event
}

// New returns a stub Injector with an already-closed events channel.
func New() *Injector {
	ch := make(chan inject.Event)
	close(ch)
	return &Injector{events: ch}
}

func (i *Injector) Start(_ context.Context, _ *scenario.Scenario) ([]string, error) {
	return nil, ErrUnsupported
}

func (i *Injector) Stop(_ context.Context) error { return nil }
func (i *Injector) Events() <-chan inject.Event  { return i.events }
func (i *Injector) SetTargetPID(_ int) error     { return ErrUnsupported }
