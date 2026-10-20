package inject_test

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

// The report/v1 events array serializes inject.Event. Fault fields added
// after v1 are omitted when unset, so an event of a scenario that doesn't
// use them stays byte-identical.
func TestEventJSONUnchangedForExistingFaults(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		ev   inject.Event
		want string
	}{
		{
			name: "http",
			ev: inject.Event{
				Experiment: "rate-limited-openai",
				Fault:      faulttypes.Fault{HTTPStatus: 429, ResponseHeaders: map[string]string{"Retry-After": "30"}},
				Fired:      true,
				Host:       "api.openai.com",
				Path:       "/v1/chat/completions",
				Timestamp:  at,
			},
			want: `{"experiment":"rate-limited-openai","fault":{"HTTPStatus":429,"ResponseHeaders":{"Retry-After":"30"},"ResponseBody":"","StreamCutoffTokens":0,"Errno":""},"fired":true,"host":"api.openai.com","path":"/v1/chat/completions","timestamp":"2026-01-02T03:04:05Z"}`,
		},
		{
			name: "syscall",
			ev: inject.Event{
				Experiment: "tcp-recv-reset",
				Fault:      faulttypes.Fault{Errno: "ECONNRESET"},
				Fired:      true,
				Syscall:    "recvmsg",
				PID:        42,
				Timestamp:  at,
			},
			want: `{"experiment":"tcp-recv-reset","fault":{"HTTPStatus":0,"ResponseHeaders":null,"ResponseBody":"","StreamCutoffTokens":0,"Errno":"ECONNRESET"},"fired":true,"syscall":"recvmsg","pid":42,"timestamp":"2026-01-02T03:04:05Z"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.ev)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("event JSON changed:\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// A response-mutation event carries the new field, after the v1 HTTP
// fields, and nothing else changes.
func TestEventJSONResponseMutation(t *testing.T) {
	ev := inject.Event{
		Experiment: "schema-mismatch",
		Fault:      faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"},
		Fired:      true,
		Host:       "mcp.example.com",
		Path:       "/mcp",
		Timestamp:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	const want = `{"experiment":"schema-mismatch","fault":{"HTTPStatus":0,"ResponseHeaders":null,"ResponseBody":"","StreamCutoffTokens":0,"ResponseMutation":"mcp-schema-mismatch","Errno":""},"fired":true,"host":"mcp.example.com","path":"/mcp","timestamp":"2026-01-02T03:04:05Z"}`
	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("event JSON:\n got %s\nwant %s", got, want)
	}
}

func TestAvailableModesIncludesBothKnown(t *testing.T) {
	got := inject.AvailableModes()
	seen := map[inject.Mode]bool{}
	for _, r := range got {
		seen[r.Mode] = true
	}
	for _, want := range []inject.Mode{inject.ModeProxy, inject.ModeEBPF} {
		if !seen[want] {
			t.Errorf("AvailableModes missing %q (got %+v)", want, got)
		}
	}
}

func TestProxyAlwaysAvailable(t *testing.T) {
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeProxy && !r.Available {
			t.Errorf("proxy mode should always be available; got %+v", r)
		}
	}
}

func TestEBPFUnavailableOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux-specific check inverted; eBPF availability depends on caps")
	}
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeEBPF && r.Available {
			t.Errorf("eBPF should be unavailable on %s; got %+v", runtime.GOOS, r)
		}
	}
}
