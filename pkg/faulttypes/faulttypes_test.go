package faulttypes_test

import (
	"errors"
	"testing"

	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
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
