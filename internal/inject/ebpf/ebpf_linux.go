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

// Scenario gate values. The BPF program hooks both recvmsg and
// recvfrom; flakyNetworkSyscall is the nominal syscall reported in
// events (per the YAML), not a guess at which kprobe fired.
const (
	flakyNetworkSyscall = "recvmsg"
	flakyNetworkErrno   = "ECONNRESET"
)

// Injector implements inject.Injector for syscall-level fault injection.
type Injector struct {
	objs  *flakyNetworkObjects
	links []link.Link

	probabilityPerThousand uint32
	scenarioName           string
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

// Start loads the BPF program and attaches the four kprobes.
func (i *Injector) Start(_ context.Context, s *scenario.Scenario) ([]string, error) {
	if i.objs != nil {
		return nil, errors.New("ebpf: already started")
	}
	exp, err := firstSyscallExp(s)
	if err != nil {
		return nil, err
	}
	if exp.Match.Syscall != flakyNetworkSyscall || exp.Fault.Errno != flakyNetworkErrno {
		return nil, fmt.Errorf("ebpf: no eBPF program for syscall=%q errno=%q", exp.Match.Syscall, exp.Fault.Errno)
	}

	i.probabilityPerThousand = uint32(exp.Probability * 1000)
	i.scenarioName = s.Name

	allowSelfMemRead()

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("ebpf: rlimit memlock: %w", err)
	}

	objs := &flakyNetworkObjects{}
	if err := loadFlakyNetworkObjects(objs, nil); err != nil {
		return nil, fmt.Errorf("ebpf: load objects: %w", err)
	}
	i.objs = objs

	attaches := []struct {
		name string
		open func() (link.Link, error)
	}{
		{"kprobe __x64_sys_recvmsg", func() (link.Link, error) {
			return link.Kprobe("__x64_sys_recvmsg", objs.FlakyNetworkRecvmsg, nil)
		}},
		{"kprobe __x64_sys_recvfrom", func() (link.Link, error) {
			return link.Kprobe("__x64_sys_recvfrom", objs.FlakyNetworkRecvfrom, nil)
		}},
		{"kprobe wake_up_new_task", func() (link.Link, error) {
			return link.Kprobe("wake_up_new_task", objs.FlakyNetworkTrackFork, nil)
		}},
		{"kprobe do_exit", func() (link.Link, error) {
			return link.Kprobe("do_exit", objs.FlakyNetworkTrackExit, nil)
		}},
	}
	for _, a := range attaches {
		l, err := a.open()
		if err != nil {
			i.closeLinks()
			_ = objs.Close()
			i.objs = nil
			return nil, fmt.Errorf("ebpf: attach %s: %w", a.name, err)
		}
		i.links = append(i.links, l)
	}

	i.pollerStop = make(chan struct{})
	i.pollerDone = make(chan struct{})
	go i.pollFaultCount()

	return nil, nil
}

func (i *Injector) closeLinks() {
	for _, l := range i.links {
		_ = l.Close()
	}
	i.links = nil
}

// SetTargetPID populates the BPF fault_config map for pid so the
// kprobe fires probabilistically for that process.
func (i *Injector) SetTargetPID(pid int) error {
	if i.objs == nil {
		return errors.New("ebpf: SetTargetPID before Start")
	}
	if pid < 0 {
		return fmt.Errorf("ebpf: invalid pid %d", pid)
	}
	key := uint32(pid) // #nosec G115 -- pid is bounded by Linux PID_MAX (4194304); explicit guard above rejects negatives.
	val := i.probabilityPerThousand
	if err := i.objs.FaultConfig.Update(&key, &val, cebpf.UpdateAny); err != nil {
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
			if err := i.objs.FaultCount.Lookup(&zero, &cur); err != nil {
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
					Syscall:    flakyNetworkSyscall,
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

// Stop detaches the kprobe and closes the BPF objects. Idempotent.
func (i *Injector) Stop(_ context.Context) error {
	i.stopOnce.Do(func() {
		if i.pollerStop != nil {
			close(i.pollerStop)
			<-i.pollerDone
		}
		for _, l := range i.links {
			if err := l.Close(); err != nil && i.stopErr == nil {
				i.stopErr = err
			}
		}
		i.links = nil
		if i.objs != nil {
			if err := i.objs.Close(); err != nil && i.stopErr == nil {
				i.stopErr = err
			}
		}
		close(i.events)
	})
	return i.stopErr
}

// Events returns the buffered fault-decision channel.
func (i *Injector) Events() <-chan inject.Event { return i.events }

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
