package proxy

import (
	"encoding/json"
	"testing"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// Migrating the HTTP builtins to the failure-mode model must produce the same
// expanded experiments (host, path, status) the hand-written per-provider YAML
// used to. This guards the refactor: it is green before and after migration.
func TestExpandBuiltinsParity(t *testing.T) {
	cases := []struct {
		builtin    string
		hostPath   map[string]string
		wantStatus int // 0 = streaming-cutoff (no status)
	}{
		{"llm-api-degraded", map[string]string{"api.openai.com": "/v1/*", "api.anthropic.com": "/v1/*"}, 429},
		{"malformed-json-response", map[string]string{"api.openai.com": "/v1/chat/completions", "api.anthropic.com": "/v1/messages"}, 200},
		{"llm-streaming-cutoff", map[string]string{"api.openai.com": "/v1/chat/completions", "api.anthropic.com": "/v1/messages"}, 0},
	}
	for _, c := range cases {
		t.Run(c.builtin, func(t *testing.T) {
			s, err := scenario.LoadBuiltin(c.builtin)
			if err != nil {
				t.Fatalf("LoadBuiltin: %v", err)
			}
			exp, err := expandScenario(s, "")
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			got := map[string]string{}
			for _, e := range exp.Experiments {
				got[e.Match.Host] = e.Match.Path
				if c.wantStatus != 0 && e.Fault.HTTPStatus != c.wantStatus {
					t.Errorf("host %s: status=%d, want %d", e.Match.Host, e.Fault.HTTPStatus, c.wantStatus)
				}
				if c.wantStatus == 0 && e.Fault.StreamCutoffTokens == 0 {
					t.Errorf("host %s: expected a stream cutoff", e.Match.Host)
				}
				// malformed-json bodies must be a valid envelope.
				if c.builtin == "malformed-json-response" {
					var env map[string]any
					if err := json.Unmarshal([]byte(e.Fault.ResponseBody), &env); err != nil {
						t.Errorf("host %s: malformed-json envelope must be valid JSON: %v", e.Match.Host, err)
					}
				}
			}
			if len(got) != len(c.hostPath) {
				t.Fatalf("got %d providers %v, want %d", len(got), got, len(c.hostPath))
			}
			for h, p := range c.hostPath {
				if got[h] != p {
					t.Errorf("host %s: path=%q, want %q", h, got[h], p)
				}
			}
		})
	}
}
