//go:build linux

package ebpf

import (
	"context"
	"os"
	"testing"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

func TestFirstSyscallExp(t *testing.T) {
	cases := []struct {
		name    string
		s       *scenario.Scenario
		wantP   float64
		wantErr bool
	}{
		{
			name: "first syscall experiment wins",
			s: &scenario.Scenario{
				Name: "x",
				Experiments: []scenario.Experiment{{
					Name:        "a",
					Fault:       faulttypes.Fault{Errno: "ECONNRESET"},
					Match:       scenario.Match{Syscall: "recvmsg"},
					Probability: 0.1,
				}},
			},
			wantP: 0.1,
		},
		{
			name: "no syscall experiments errors",
			s: &scenario.Scenario{
				Name: "y",
				Experiments: []scenario.Experiment{{
					Name:        "a",
					Fault:       faulttypes.Fault{HTTPStatus: 429},
					Match:       scenario.Match{Host: "api.openai.com"},
					Probability: 0.1,
				}},
			},
			wantErr: true,
		},
		{name: "nil scenario", s: nil, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := firstSyscallExp(c.s)
			if c.wantErr {
				if err == nil {
					t.Errorf("got nil err, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Probability != c.wantP {
				t.Errorf("got %v, want %v", got.Probability, c.wantP)
			}
		})
	}
}

func TestStartRejectsUnsupportedScenario(t *testing.T) {
	inj := New()
	t.Cleanup(func() { _ = inj.Stop(context.Background()) })

	s := &scenario.Scenario{
		Name: "tool-permission-denied",
		Experiments: []scenario.Experiment{{
			Name:        "x",
			Fault:       faulttypes.Fault{Errno: "EACCES"},
			Match:       scenario.Match{Syscall: "openat"},
			Probability: 0.1,
		}},
	}
	if _, err := inj.Start(context.Background(), s); err == nil {
		t.Errorf("expected error for unimplemented scenario, got nil")
	}
}

// TestStartLoadsKernelProgram exercises the full BPF load path. Skipped
// unless running as root or with CAP_BPF, since loading BPF programs
// is a privileged operation.
func TestStartLoadsKernelProgram(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root or CAP_BPF; run with sudo to exercise the BPF loader")
	}
	inj := New()
	t.Cleanup(func() { _ = inj.Stop(context.Background()) })

	s := &scenario.Scenario{
		Name: "flaky-network",
		Experiments: []scenario.Experiment{{
			Name:        "tcp-recv-reset",
			Fault:       faulttypes.Fault{Errno: "ECONNRESET"},
			Match:       scenario.Match{Syscall: "recvmsg"},
			Probability: 0.1,
		}},
	}
	if _, err := inj.Start(context.Background(), s); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if inj.objs == nil || len(inj.links) == 0 {
		t.Errorf("expected objs and links to be set after Start")
	}
}
