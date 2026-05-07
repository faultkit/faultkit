package scenario_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
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

func TestLoadBytesValid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"http", validHTTPYAML, "my-scenario"},
		{"syscall", validSyscallYAML, "net-flaky"},
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
