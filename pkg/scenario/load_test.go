package scenario_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

const validHTTPYAML = `
name: my-scenario
description: A test
experiments:
  - name: my-exp
    fault:
      http_status: 429
    match:
      host: api.openai.com
    probability: 0.2
`

const validSyscallYAML = `
name: net-flaky
requires:
  platform: linux
  kernel_min: "5.8"
experiments:
  - name: rst
    fault:
      errno: ECONNRESET
    match:
      syscall: recvmsg
    probability: 0.1
`

const validFailureYAML = `
name: my-scenario
experiments:
  - name: my-exp
    failure: malformed-json
    probability: 0.2
`

const validFailureProviderYAML = `
name: my-scenario
experiments:
  - name: my-exp
    failure: rate-limited
    provider: anthropic
    probability: 0.2
`

func TestLoadBytesValid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"http", validHTTPYAML, "my-scenario"},
		{"syscall", validSyscallYAML, "net-flaky"},
		{"failure mode", validFailureYAML, "my-scenario"},
		{"failure mode with provider", validFailureProviderYAML, "my-scenario"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := scenario.LoadBytes([]byte(c.yaml))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.Name != c.want {
				t.Errorf("Name = %q, want %q", s.Name, c.want)
			}
		})
	}
}

func TestLoadBytesInvalid(t *testing.T) {
	cases := []struct {
		name         string
		yaml         string
		wantSentinel error
		wantSubstr   string
	}{
		{
			name:       "bad yaml",
			yaml:       "not: : valid:::\n  - syntax",
			wantSubstr: "parsing scenario yaml",
		},
		{
			name:       "missing name",
			yaml:       "experiments:\n  - name: x\n    fault: {http_status: 500}\n    match: {host: a}\n    probability: 0.1\n",
			wantSubstr: "kebab-case",
		},
		{
			name:       "no experiments",
			yaml:       "name: empty\n",
			wantSubstr: "no experiments",
		},
		{
			name:         "mixed fault",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    fault: {http_status: 500, errno: EIO}\n    match: {host: a}\n    probability: 0.1\n",
			wantSentinel: faulttypes.ErrFaultMixed,
		},
		{
			name:         "mixed match",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    fault: {http_status: 500}\n    match: {host: a, syscall: recvmsg}\n    probability: 0.1\n",
			wantSentinel: scenario.ErrMatchMixed,
		},
		{
			name:       "fault http but match syscall",
			yaml:       "name: bad\nexperiments:\n  - name: x\n    fault: {http_status: 500}\n    match: {syscall: recvmsg}\n    probability: 0.1\n",
			wantSubstr: "must both be HTTP-level or both syscall-level",
		},
		{
			name:       "probability out of range",
			yaml:       "name: bad\nexperiments:\n  - name: x\n    fault: {http_status: 500}\n    match: {host: a}\n    probability: 2.0\n",
			wantSubstr: "probability",
		},
		{
			name:         "failure and raw fault both set",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    failure: malformed-json\n    fault: {http_status: 500}\n    match: {host: a}\n    probability: 0.1\n",
			wantSentinel: scenario.ErrExperimentMixed,
		},
		{
			name:         "provider without failure",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    fault: {http_status: 500}\n    match: {host: a}\n    provider: anthropic\n    probability: 0.1\n",
			wantSentinel: scenario.ErrProviderNeedsFailure,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := scenario.LoadBytes([]byte(c.yaml))
			if err == nil {
				t.Fatalf("got nil, want error")
			}
			if c.wantSentinel != nil && !errors.Is(err, c.wantSentinel) {
				t.Fatalf("err %v not wrapping %v", err, c.wantSentinel)
			}
			if c.wantSubstr != "" && !strings.Contains(err.Error(), c.wantSubstr) {
				t.Fatalf("err %q does not contain %q", err.Error(), c.wantSubstr)
			}
		})
	}
}

// delayYAML is a one-experiment raw scenario with the given flow-style
// fault and match.
func delayYAML(fault, match string) string {
	return fmt.Sprintf("name: delay\nexperiments:\n  - name: x\n    fault: %s\n    match: %s\n    probability: 1\n", fault, match)
}

// delay_ms and delay_jitter_ms are level-neutral: a delay pairs with an
// HTTP or a syscall match, and only the HTTP and syscall fields must agree
// with the match. A fixture-driven experiment still takes no fault fields.
func TestValidateDelay(t *testing.T) {
	const (
		httpMatch    = "{host: api.openai.com}"
		syscallMatch = "{syscall: write}"
		levelErr     = "must both be HTTP-level or both syscall-level"
	)
	cases := []struct {
		name         string
		yaml         string
		wantSentinel error
		wantSubstr   string
	}{
		{name: "delay only with an http match", yaml: delayYAML("{delay_ms: 8000, delay_jitter_ms: 7000}", httpMatch)},
		{name: "delay only with a syscall match", yaml: delayYAML("{delay_ms: 500}", syscallMatch)},
		{name: "delay and status with an http match", yaml: delayYAML("{delay_ms: 500, http_status: 503}", httpMatch)},
		{name: "delay and stream cutoff with an http match", yaml: delayYAML("{delay_ms: 500, stream_cutoff_tokens: 80}", httpMatch)},
		{name: "delay and errno with a syscall match", yaml: delayYAML("{delay_ms: 500, errno: EIO}", syscallMatch)},
		{name: "delay and status with a syscall match", yaml: delayYAML("{delay_ms: 500, http_status: 503}", syscallMatch), wantSubstr: levelErr},
		{name: "delay and errno with an http match", yaml: delayYAML("{delay_ms: 500, errno: EIO}", httpMatch), wantSubstr: levelErr},
		{name: "jitter without delay", yaml: delayYAML("{delay_jitter_ms: 100}", httpMatch), wantSubstr: "delay_jitter_ms requires delay_ms"},
		{name: "negative delay", yaml: delayYAML("{delay_ms: -1}", httpMatch), wantSubstr: "must be non-negative"},
		{name: "negative jitter", yaml: delayYAML("{delay_ms: 500, delay_jitter_ms: -1}", httpMatch), wantSubstr: "must be non-negative"},
		{name: "delay over the cap", yaml: delayYAML("{delay_ms: 290000, delay_jitter_ms: 10001}", httpMatch), wantSubstr: "must not exceed 300000"},
		{
			name:         "failure mode with a delay",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    failure: slow-first-token\n    fault: {delay_ms: 500}\n    probability: 0.1\n",
			wantSentinel: scenario.ErrExperimentMixed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := scenario.LoadBytes([]byte(c.yaml))
			wantErr := c.wantSentinel != nil || c.wantSubstr != ""
			if !wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want error")
			}
			if c.wantSentinel != nil && !errors.Is(err, c.wantSentinel) {
				t.Fatalf("err %v not wrapping %v", err, c.wantSentinel)
			}
			if c.wantSubstr != "" && !strings.Contains(err.Error(), c.wantSubstr) {
				t.Fatalf("err %q does not contain %q", err.Error(), c.wantSubstr)
			}
		})
	}
}

// The YAML keys reach the Fault fields.
func TestLoadBytesDelayFields(t *testing.T) {
	s, err := scenario.LoadBytes([]byte(delayYAML("{delay_ms: 8000, delay_jitter_ms: 7000}", "{host: api.openai.com}")))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got := s.Experiments[0].Fault; got.DelayMS != 8000 || got.DelayJitterMS != 7000 {
		t.Errorf("DelayMS, DelayJitterMS = %d, %d; want 8000, 7000", got.DelayMS, got.DelayJitterMS)
	}
}

// methodYAML returns a one-experiment scenario with an HTTP fault and the
// match fields in match, a flow-mapping body such as "host: a, method: POST".
func methodYAML(match string) string {
	return "name: m\nexperiments:\n  - name: x\n    fault: {http_status: 200}\n    match: {" + match + "}\n    probability: 1\n"
}

// match.method accepts the allowlisted methods in any case and keeps the
// value as written; matching compares it case-insensitively.
func TestLoadBytesMatchMethod(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "post", "Patch"} {
		t.Run(method, func(t *testing.T) {
			s, err := scenario.LoadBytes([]byte(methodYAML("host: store.example.com, path: /v1/memories, method: " + method)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := s.Experiments[0].Match.Method; got != method {
				t.Errorf("Match.Method = %q, want %q", got, method)
			}
		})
	}
}

func TestLoadBytesMatchMethodInvalid(t *testing.T) {
	cases := []struct {
		name         string
		yaml         string
		wantSentinel error
		wantSubstr   string
	}{
		{name: "CONNECT not allowed", yaml: methodYAML("host: a, method: CONNECT"), wantSubstr: `match method "CONNECT" is not one of`},
		{name: "TRACE not allowed", yaml: methodYAML("host: a, method: TRACE"), wantSubstr: `match method "TRACE" is not one of GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS`},
		{name: "typo", yaml: methodYAML("host: a, method: PSOT"), wantSubstr: `match method "PSOT" is not one of`},
		{name: "padded", yaml: methodYAML(`host: a, method: " POST"`), wantSubstr: `match method " POST" is not one of`},
		{name: "long s folds to POST", yaml: methodYAML("host: a, method: poſt"), wantSubstr: `match method "poſt" is not one of`},
		{name: "dotless i uppercases to OPTIONS", yaml: methodYAML("host: a, method: optıons"), wantSubstr: `match method "optıons" is not one of`},
		{name: "method and syscall", yaml: methodYAML("method: POST, syscall: recvmsg"), wantSentinel: scenario.ErrMatchMixed},
		{name: "method alone", yaml: methodYAML("method: POST"), wantSentinel: scenario.ErrMatchEmpty},
		{
			name:         "method on a fixture-driven experiment",
			yaml:         "name: bad\nexperiments:\n  - name: x\n    failure: rate-limited\n    match: {method: POST}\n    probability: 0.1\n",
			wantSentinel: scenario.ErrExperimentMixed,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := scenario.LoadBytes([]byte(c.yaml))
			if err == nil {
				t.Fatalf("got nil, want error")
			}
			if c.wantSentinel != nil && !errors.Is(err, c.wantSentinel) {
				t.Fatalf("err %v not wrapping %v", err, c.wantSentinel)
			}
			if c.wantSubstr != "" && !strings.Contains(err.Error(), c.wantSubstr) {
				t.Fatalf("err %q does not contain %q", err.Error(), c.wantSubstr)
			}
		})
	}
}
