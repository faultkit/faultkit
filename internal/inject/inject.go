// Package inject defines the contract every fault-injection mechanism
// (proxy, eBPF, future shim) implements. The CLI's `run` command picks
// an Injector based on the scenario and the host's capabilities, then
// drives it through the lifecycle below.
package inject

import (
	"context"
	"time"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

// Event reports a fault decision made during a run. Injectors emit one
// Event per matched-and-evaluated experiment occurrence (a request in
// proxy mode, a syscall in eBPF mode). Fired distinguishes "the fault
// actually applied" from "we matched but the dice roll didn't fire".
type Event struct {
	Experiment string
	Fault      faulttypes.Fault
	Fired      bool
	Host       string
	Path       string
	Syscall    string
	PID        int
	Timestamp  time.Time
	Err        string
}

// Injector starts a fault-injection mechanism, runs until Stop is
// called, and emits Events as faults are evaluated.
//
// Start returns a slice of `KEY=VALUE` env entries the runner must
// merge into the target's environment so the target's traffic reaches
// the injector. Empty slice is allowed (eBPF mode needs no env vars).
//
// The Events channel must remain open while the Injector is running
// and is closed by Stop.
type Injector interface {
	Start(ctx context.Context, s *scenario.Scenario) ([]string, error)
	Stop(ctx context.Context) error
	Events() <-chan Event
}
