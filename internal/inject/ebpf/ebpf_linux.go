//go:build linux

package ebpf

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	cebpf "github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

// prSetDumpable is the prctl(2) op number; not exported by the
// stdlib's syscall package on its own.
const prSetDumpable = 4

// faultPollInterval bounds event-emit latency without spinning. The
// kernel-side override_return is the actual fault path; the poller
// only synthesizes user-facing events from a counter.
const faultPollInterval = 50 * time.Millisecond

// Scenario gate values. The gate keys the dispatch in Start: a
// scenario whose first syscall experiment matches one of these pairs
// gets the corresponding BPF program loaded. Anything else is rejected.
const (
	flakyNetworkSyscall = "recvmsg"
	flakyNetworkErrno   = "ECONNRESET"
	toolPermSyscall     = "openat"
	toolPermErrno       = "EACCES"
)

// Injector implements inject.Injector for syscall-level fault injection.
type Injector struct {
	closeObjs   func() error
	faultConfig *cebpf.Map
	faultCount  *cebpf.Map
	links       []link.Link

	probabilityPerThousand uint32
	scenarioName           string
	syscallName            string
	events                 chan inject.Event

	pollerStop chan struct{}
	pollerDone chan struct{}

	stopOnce sync.Once
	stopErr  error
}

// New returns a new, unstarted eBPF Injector.
func New() *Injector {
	return &Injector{events: make(chan inject.Event, 256)}
}

// Start loads the BPF program matching the scenario and attaches its
// kprobes. Returns an empty env slice — eBPF mode needs no env vars.
func (i *Injector) Start(_ context.Context, s *scenario.Scenario) ([]string, error) {
	if i.closeObjs != nil {
		return nil, errors.New("ebpf: already started")
	}
	exp, err := firstSyscallExp(s)
	if err != nil {
		return nil, err
	}

	i.probabilityPerThousand = uint32(exp.Probability * 1000)
	i.scenarioName = s.Name
	i.syscallName = exp.Match.Syscall

	allowSelfMemRead()

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("ebpf: rlimit memlock: %w", err)
	}

	switch {
	case exp.Match.Syscall == flakyNetworkSyscall && exp.Fault.Errno == flakyNetworkErrno:
		err = i.loadFlakyNetwork()
	case exp.Match.Syscall == toolPermSyscall && exp.Fault.Errno == toolPermErrno:
		err = i.loadToolPermDenied()
	default:
		return nil, fmt.Errorf("ebpf: no eBPF program for syscall=%q errno=%q", exp.Match.Syscall, exp.Fault.Errno)
	}
	if err != nil {
		return nil, err
	}

	i.pollerStop = make(chan struct{})
	i.pollerDone = make(chan struct{})
	go i.pollFaultCount()

	return nil, nil
}

func (i *Injector) loadFlakyNetwork() error {
	objs := &flakyNetworkObjects{}
	if err := loadFlakyNetworkObjects(objs, nil); err != nil {
		return fmt.Errorf("ebpf: load flaky_network: %w", err)
	}
	i.closeObjs = objs.Close
	i.faultConfig = objs.FaultConfig
	i.faultCount = objs.FaultCount

	return i.attachKprobes([]kprobeTarget{
		{"__x64_sys_recvmsg", objs.FlakyNetworkRecvmsg},
		{"__x64_sys_recvfrom", objs.FlakyNetworkRecvfrom},
		{"wake_up_new_task", objs.FlakyNetworkTrackFork},
		{"do_exit", objs.FlakyNetworkTrackExit},
	})
}

func (i *Injector) loadToolPermDenied() error {
	objs := &toolPermDeniedObjects{}
	if err := loadToolPermDeniedObjects(objs, nil); err != nil {
		return fmt.Errorf("ebpf: load tool_perm_denied: %w", err)
	}
	i.closeObjs = objs.Close
	i.faultConfig = objs.FaultConfig
	i.faultCount = objs.FaultCount

	return i.attachKprobes([]kprobeTarget{
		{"__x64_sys_openat", objs.ToolPermDeniedOpenat},
		{"wake_up_new_task", objs.ToolPermDeniedTrackFork},
		{"do_exit", objs.ToolPermDeniedTrackExit},
	})
}

type kprobeTarget struct {
	fn   string
	prog *cebpf.Program
}

func (i *Injector) attachKprobes(targets []kprobeTarget) error {
	for _, t := range targets {
		l, err := link.Kprobe(t.fn, t.prog, nil)
		if err != nil {
			i.closeLinks()
			if i.closeObjs != nil {
				_ = i.closeObjs()
				i.closeObjs = nil
			}
			return fmt.Errorf("ebpf: attach kprobe %s: %w", t.fn, err)
		}
		i.links = append(i.links, l)
	}
	return nil
}

// SetTargetPID populates the BPF fault_config map for pid so the
// kprobe fires probabilistically for that process.
func (i *Injector) SetTargetPID(pid int) error {
	if i.faultConfig == nil {
		return errors.New("ebpf: SetTargetPID before Start")
	}
	if pid < 0 {
		return fmt.Errorf("ebpf: invalid pid %d", pid)
	}
	key := uint32(pid) // #nosec G115 -- pid is bounded by Linux PID_MAX (4194304); explicit guard above rejects negatives.
	val := i.probabilityPerThousand
	if err := i.faultConfig.Update(&key, &val, cebpf.UpdateAny); err != nil {
		return fmt.Errorf("ebpf: configure pid %d: %w", pid, err)
	}
	return nil
}

func (i *Injector) pollFaultCount() {
	defer close(i.pollerDone)

	t := time.NewTicker(faultPollInterval)
	defer t.Stop()

	var prev uint64
	zero := uint32(0)
	for {
		select {
		case <-i.pollerStop:
			return
		case <-t.C:
			var cur uint64
			if err := i.faultCount.Lookup(&zero, &cur); err != nil {
				continue
			}
			if cur < prev {
				prev = cur
				continue
			}
			delta := cur - prev
			prev = cur
			for n := uint64(0); n < delta; n++ {
				ev := inject.Event{
					Experiment: i.scenarioName,
					Fired:      true,
					Syscall:    i.syscallName,
					Timestamp:  time.Now(),
				}
				select {
				case i.events <- ev:
				default:
				}
			}
		}
	}
}

// Stop detaches the kprobes and closes the BPF objects. Idempotent.
func (i *Injector) Stop(_ context.Context) error {
	i.stopOnce.Do(func() {
		if i.pollerStop != nil {
			close(i.pollerStop)
			<-i.pollerDone
		}
		i.closeLinks()
		if i.closeObjs != nil {
			if err := i.closeObjs(); err != nil {
				i.stopErr = err
			}
		}
		close(i.events)
	})
	return i.stopErr
}

// Events returns the buffered fault-decision channel.
func (i *Injector) Events() <-chan inject.Event { return i.events }

func (i *Injector) closeLinks() {
	for _, l := range i.links {
		_ = l.Close()
	}
	i.links = nil
}

// allowSelfMemRead lifts the kernel's `dumpable` flag back to 1 so
// /proc/self/mem is readable from this process. cilium/ebpf needs that
// for kernel-version detection on load. No-op when already dumpable
// (running as root, or no file caps in play); when running with file
// caps the prctl requires CAP_SYS_PTRACE to succeed.
//
// Trade-off: with dumpable=1, other same-user processes can ptrace
// this process and read its memory while it's running. faultkit is a
// developer tool — the assumed environment is a developer's machine
// or a single-tenant CI runner with no hostile co-tenants. If that
// doesn't fit, run faultkit under sudo instead and skip the file-caps
// path entirely.
func allowSelfMemRead() {
	_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetDumpable, 1, 0)
}

func firstSyscallExp(s *scenario.Scenario) (*scenario.Experiment, error) {
	if s == nil {
		return nil, errors.New("ebpf: nil scenario")
	}
	for i := range s.Experiments {
		if s.Experiments[i].Match.IsSyscall() {
			return &s.Experiments[i], nil
		}
	}
	return nil, fmt.Errorf("scenario %q has no syscall-level experiments", s.Name)
}
