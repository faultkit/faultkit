package cli

import (
	"testing"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: pickInjector is unexported; test its --base-url branch logic.
func TestPickInjectorBaseURL(t *testing.T) {
	httpScn := &scenario.Scenario{
		Name: "http",
		Experiments: []scenario.Experiment{{
			Name: "x", Fault: faulttypes.Fault{HTTPStatus: 429},
			Match: scenario.Match{Host: "api.openai.com"}, Probability: 1,
		}},
	}
	sysScn := &scenario.Scenario{
		Name: "sys",
		Experiments: []scenario.Experiment{{
			Name: "x", Fault: faulttypes.Fault{Errno: "ECONNRESET"},
			Match: scenario.Match{Syscall: "recvmsg"}, Probability: 1,
		}},
	}

	if _, err := pickInjector(httpScn, modeProxy, true, ""); err != nil {
		t.Errorf("base-url + proxy: unexpected error: %v", err)
	}
	if _, err := pickInjector(sysScn, modeAuto, true, ""); err == nil {
		t.Error("base-url + syscall-only scenario should be a usage error")
	}
	if _, err := pickInjector(sysScn, modeEBPF, true, ""); err == nil {
		t.Error("base-url + --mode=ebpf should be a usage error")
	}
}

// White-box: pickInjector's --provider branch logic.
func TestPickInjectorProvider(t *testing.T) {
	httpScn := &scenario.Scenario{
		Name:        "http",
		Experiments: []scenario.Experiment{{Name: "x", Failure: "rate-limited", Probability: 1}},
	}
	sysScn := &scenario.Scenario{
		Name: "sys",
		Experiments: []scenario.Experiment{{
			Name: "x", Fault: faulttypes.Fault{Errno: "ECONNRESET"},
			Match: scenario.Match{Syscall: "recvmsg"}, Probability: 1,
		}},
	}

	if _, err := pickInjector(httpScn, modeAuto, false, "anthropic"); err != nil {
		t.Errorf("provider + http: unexpected error: %v", err)
	}
	if _, err := pickInjector(httpScn, modeAuto, false, "bogus"); err == nil {
		t.Error("unknown provider should be a usage error")
	}
	if _, err := pickInjector(sysScn, modeAuto, false, "openai"); err == nil {
		t.Error("provider + syscall-only scenario should be a usage error")
	}
}
