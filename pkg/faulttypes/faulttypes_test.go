package faulttypes_test

import (
	"errors"
	"math"
	"testing"

	"github.com/faultkit/faultkit/pkg/faulttypes"
)

func TestFaultValidate(t *testing.T) {
	tests := []struct {
		name         string
		fault        faulttypes.Fault
		wantSentinel error
		wantAnyErr   bool
	}{
		{name: "http status only", fault: faulttypes.Fault{HTTPStatus: 429}},
		{name: "http body only", fault: faulttypes.Fault{ResponseBody: "boom"}},
		{name: "http headers only", fault: faulttypes.Fault{ResponseHeaders: map[string]string{"X": "Y"}}},
		{name: "syscall only", fault: faulttypes.Fault{Errno: "ECONNRESET"}},
		{name: "stream cutoff only", fault: faulttypes.Fault{StreamCutoffTokens: 80}},
		{name: "mixed", fault: faulttypes.Fault{HTTPStatus: 429, Errno: "ECONNRESET"}, wantSentinel: faulttypes.ErrFaultMixed},
		{name: "stream cutoff mixed with errno", fault: faulttypes.Fault{StreamCutoffTokens: 80, Errno: "EIO"}, wantSentinel: faulttypes.ErrFaultMixed},
		{name: "empty", fault: faulttypes.Fault{}, wantSentinel: faulttypes.ErrFaultEmpty},
		{name: "status too low", fault: faulttypes.Fault{HTTPStatus: 99}, wantAnyErr: true},
		{name: "status too high", fault: faulttypes.Fault{HTTPStatus: 600}, wantAnyErr: true},
		{name: "negative stream cutoff", fault: faulttypes.Fault{StreamCutoffTokens: -1}, wantAnyErr: true},
		{name: "delay only", fault: faulttypes.Fault{DelayMS: 50}},
		{name: "delay with jitter", fault: faulttypes.Fault{DelayMS: 50, DelayJitterMS: 20}},
		{name: "delay with http status", fault: faulttypes.Fault{DelayMS: 50, HTTPStatus: 503}},
		{name: "delay with stream cutoff", fault: faulttypes.Fault{DelayMS: 50, StreamCutoffTokens: 80}},
		{name: "delay with errno", fault: faulttypes.Fault{DelayMS: 50, Errno: "EIO"}},
		{name: "delay at the cap", fault: faulttypes.Fault{DelayMS: 200000, DelayJitterMS: 100000}},
		{name: "delay over the cap", fault: faulttypes.Fault{DelayMS: 200000, DelayJitterMS: 100001}, wantAnyErr: true},
		{name: "delay alone over the cap", fault: faulttypes.Fault{DelayMS: 300001}, wantAnyErr: true},
		{name: "jitter that would overflow the sum", fault: faulttypes.Fault{DelayMS: 1, DelayJitterMS: math.MaxInt}, wantAnyErr: true},
		{name: "jitter without delay", fault: faulttypes.Fault{DelayJitterMS: 20}, wantAnyErr: true},
		{name: "jitter without delay on an http fault", fault: faulttypes.Fault{HTTPStatus: 503, DelayJitterMS: 20}, wantAnyErr: true},
		{name: "negative delay", fault: faulttypes.Fault{DelayMS: -1}, wantAnyErr: true},
		{name: "negative delay on an http fault", fault: faulttypes.Fault{HTTPStatus: 503, DelayMS: -1}, wantAnyErr: true},
		{name: "negative jitter", fault: faulttypes.Fault{DelayMS: 50, DelayJitterMS: -1}, wantAnyErr: true},
		{name: "delay does not hide a mix", fault: faulttypes.Fault{DelayMS: 50, HTTPStatus: 429, Errno: "EIO"}, wantSentinel: faulttypes.ErrFaultMixed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fault.Validate()
			switch {
			case tt.wantSentinel != nil:
				if !errors.Is(err, tt.wantSentinel) {
					t.Fatalf("got %v, want %v", err, tt.wantSentinel)
				}
			case tt.wantAnyErr:
				if err == nil {
					t.Fatalf("got nil, want any error")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestFaultIsHTTPIsSyscall(t *testing.T) {
	cases := []struct {
		name           string
		f              faulttypes.Fault
		wantHTTP, wSys bool
	}{
		{"empty", faulttypes.Fault{}, false, false},
		{"status", faulttypes.Fault{HTTPStatus: 200}, true, false},
		{"errno", faulttypes.Fault{Errno: "EACCES"}, false, true},
		{"stream cutoff", faulttypes.Fault{StreamCutoffTokens: 5}, true, false},
		{"delay only", faulttypes.Fault{DelayMS: 50, DelayJitterMS: 20}, false, false},
		{"delay with status", faulttypes.Fault{DelayMS: 50, HTTPStatus: 503}, true, false},
		{"delay with errno", faulttypes.Fault{DelayMS: 50, Errno: "EIO"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.IsHTTP(); got != c.wantHTTP {
				t.Errorf("IsHTTP = %v, want %v", got, c.wantHTTP)
			}
			if got := c.f.IsSyscall(); got != c.wSys {
				t.Errorf("IsSyscall = %v, want %v", got, c.wSys)
			}
		})
	}
}

// response_mutation is an HTTP-level field. Its one value is accepted on
// its own or with a delay; any other value, or another HTTP-level field
// next to it, is rejected.
func TestFaultResponseMutation(t *testing.T) {
	const mcp = "mcp-schema-mismatch"
	tests := []struct {
		name         string
		fault        faulttypes.Fault
		wantSentinel error
		wantAnyErr   bool
	}{
		{name: "alone", fault: faulttypes.Fault{ResponseMutation: mcp}},
		{name: "with a delay", fault: faulttypes.Fault{ResponseMutation: mcp, DelayMS: 50, DelayJitterMS: 10}},
		{name: "unknown value", fault: faulttypes.Fault{ResponseMutation: "mcp-schema"}, wantAnyErr: true},
		{name: "value in another case", fault: faulttypes.Fault{ResponseMutation: "MCP-SCHEMA-MISMATCH"}, wantAnyErr: true},
		{name: "with http status", fault: faulttypes.Fault{ResponseMutation: mcp, HTTPStatus: 200}, wantAnyErr: true},
		{name: "with headers", fault: faulttypes.Fault{ResponseMutation: mcp, ResponseHeaders: map[string]string{"X": "Y"}}, wantAnyErr: true},
		{name: "with body", fault: faulttypes.Fault{ResponseMutation: mcp, ResponseBody: "{}"}, wantAnyErr: true},
		{name: "with stream cutoff", fault: faulttypes.Fault{ResponseMutation: mcp, StreamCutoffTokens: 3}, wantAnyErr: true},
		{name: "with errno", fault: faulttypes.Fault{ResponseMutation: mcp, Errno: "EIO"}, wantSentinel: faulttypes.ErrFaultMixed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fault.Validate()
			switch {
			case tt.wantSentinel != nil:
				if !errors.Is(err, tt.wantSentinel) {
					t.Fatalf("got %v, want %v", err, tt.wantSentinel)
				}
			case tt.wantAnyErr:
				if err == nil {
					t.Fatalf("got nil, want any error")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}

	if f := (faulttypes.Fault{ResponseMutation: mcp}); !f.IsHTTP() || f.IsSyscall() {
		t.Errorf("IsHTTP, IsSyscall = %v, %v; want true, false", f.IsHTTP(), f.IsSyscall())
	}
}
