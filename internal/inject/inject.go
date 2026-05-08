// Package inject defines the contract every fault-injection mechanism
// (proxy, eBPF) implements. The CLI's `run` command picks
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
	Experiment string           `json:"experiment"`
	Fault      faulttypes.Fault `json:"fault"`
	Fired      bool             `json:"fired"`
	Host       string           `json:"host,omitempty"`
	Path       string           `json:"path,omitempty"`
	Syscall    string           `json:"syscall,omitempty"`
	PID        int              `json:"pid,omitempty"`
	Timestamp  time.Time        `json:"timestamp"`
	Err        string           `json:"err,omitempty"`
}

// Injector starts a fault-injection mechanism, runs until Stop is
// called, and emits Events as faults are evaluated.
//
// Start returns a slice of `KEY=VALUE` env entries the runner must
// merge into the target's environment so the target's traffic reaches
// the injector. Empty slice is allowed (eBPF mode needs no env vars).
//
// Events returns a buffered channel of fault-decision events. The
// channel stays open while the Injector runs; Stop closes it.
// Implementations may drop events when the buffer is full so they
// never block the request hot path — consumers should drain
// continuously.
type Injector interface {
	Start(ctx context.Context, s *scenario.Scenario) ([]string, error)
	Stop(ctx context.Context) error
	Events() <-chan Event
}

// PIDAware is implemented by injectors that need the target's PID
// after the runner forks (eBPF mode populates a per-PID fault config
// map). The CLI's runner calls SetTargetPID once between cmd.Start and
// cmd.Wait. Injectors that don't care about PID (proxy mode) simply
// don't implement this interface.
type PIDAware interface {
	SetTargetPID(pid int) error
}
