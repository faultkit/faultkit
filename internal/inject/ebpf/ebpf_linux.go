//go:build linux

package ebpf

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	cebpf "github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

// prSetDumpable is the prctl(2) op number; not exported by the
// stdlib's syscall package on its own.
const prSetDumpable = 4

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
	reader      *ringbuf.Reader
	links       []link.Link

	probabilityPerThousand uint32
	scenarioName           string
	syscallName            string
	events                 chan inject.Event

	readerDone chan struct{}

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

	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("ebpf: rlimit memlock: %w", err)
	}

	// dumpable=1 is required for /proc/self/mem reads cilium/ebpf does
	// during BTF detection at load. Scope to the load call only — keep
	// the post-load process ptrace-protected for the duration of the
	// run so the in-memory CA private key isn't ptrace-readable by a
	// co-tenant. No-op when running as root (already dumpable).
	dumpableRestore := withDumpable()
	switch {
	case exp.Match.Syscall == flakyNetworkSyscall && exp.Fault.Errno == flakyNetworkErrno:
		err = i.loadFlakyNetwork()
	case exp.Match.Syscall == toolPermSyscall && exp.Fault.Errno == toolPermErrno:
		err = i.loadToolPermDenied()
	default:
		dumpableRestore()
		return nil, fmt.Errorf("ebpf: no eBPF program for syscall=%q errno=%q", exp.Match.Syscall, exp.Fault.Errno)
	}
	dumpableRestore()
	if err != nil {
		return nil, err
	}

	i.readerDone = make(chan struct{})
	go i.readEvents()

	return nil, nil
}

func (i *Injector) loadFlakyNetwork() error {
	objs := &flakyNetworkObjects{}
	if err := loadFlakyNetworkObjects(objs, nil); err != nil {
		return fmt.Errorf("ebpf: load flaky_network: %w", err)
	}
	i.closeObjs = objs.Close
	i.faultConfig = objs.FaultConfig
	if err := i.openReader(objs.FaultEvents); err != nil {
		return err
	}

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
	if err := i.openReader(objs.FaultEvents); err != nil {
		return err
	}

	return i.attachKprobes([]kprobeTarget{
		{"__x64_sys_openat", objs.ToolPermDeniedOpenat},
		{"wake_up_new_task", objs.ToolPermDeniedTrackFork},
		{"do_exit", objs.ToolPermDeniedTrackExit},
	})
}

func (i *Injector) openReader(m *cebpf.Map) error {
	r, err := ringbuf.NewReader(m)
	if err != nil {
		_ = i.closeObjs()
		i.closeObjs = nil
		return fmt.Errorf("ebpf: open ringbuf reader: %w", err)
	}
	i.reader = r
	return nil
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

// readEvents reads fault records pushed by the BPF program and
// translates each to an inject.Event. Exits when reader.Close() is
// called from Stop, which makes Read return ringbuf.ErrClosed.
func (i *Injector) readEvents() {
	defer close(i.readerDone)

	for {
		rec, err := i.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			inject.TrySend(i.events, inject.Event{
				Experiment: i.scenarioName,
				Syscall:    i.syscallName,
				Timestamp:  time.Now(),
				Err:        fmt.Sprintf("ringbuf read: %v", err),
			})
			return
		}
		ev, ok := decodeFaultEvent(rec.RawSample)
		if !ok {
			continue
		}
		inject.TrySend(i.events, inject.Event{
			Experiment: i.scenarioName,
			Fired:      true,
			Syscall:    i.syscallName,
			PID:        int(ev.pid),
			Timestamp:  time.Now(),
		})
	}
}

type faultEventRaw struct {
	tsNS uint64
	pid  uint32
}

// decodeFaultEvent matches the BPF `struct fault_event` layout
// (__u64 ts_ns, __u32 pid, __u32 _pad).
func decodeFaultEvent(b []byte) (faultEventRaw, bool) {
	if len(b) < 16 {
		return faultEventRaw{}, false
	}
	return faultEventRaw{
		tsNS: binary.LittleEndian.Uint64(b[0:8]),
		pid:  binary.LittleEndian.Uint32(b[8:12]),
	}, true
}

// Stop detaches the kprobes and closes the BPF objects. Idempotent.
func (i *Injector) Stop(_ context.Context) error {
	i.stopOnce.Do(func() {
		if i.reader != nil {
			_ = i.reader.Close()
			<-i.readerDone
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

// withDumpable lifts the kernel's `dumpable` flag to 1 so /proc/self/mem
// is readable from this process. cilium/ebpf needs that for
// kernel-version detection during BPF load. The returned restore
// function flips it back to 0 — keep the post-load process
// ptrace-protected so the in-memory CA private key (when running with
// the proxy) and other state aren't readable by a co-tenant.
//
// Skipped when already root: dumpable is already 1 there, the prctl
// would be a no-op going up and an unwanted change going down.
func withDumpable() func() {
	if os.Geteuid() == 0 {
		return func() {}
	}
	_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetDumpable, 1, 0)
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0)
	}
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
