package proxy

import (
	"testing"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// The Phase-2 builtins must expand cleanly (their failure modes exist in the
// catalog) and fan out to the expected providers.
func TestExpandNewBuiltins(t *testing.T) {
	cases := []struct {
		builtin       string
		wantProviders []string
	}{
		{"anthropic-overloaded", []string{"api.anthropic.com"}},
		{"max-tokens-truncation", []string{"api.openai.com", "api.anthropic.com"}},
		{"malformed-tool-use", []string{"api.openai.com", "api.anthropic.com"}},
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
			if len(exp.Experiments) != len(c.wantProviders) {
				t.Fatalf("got %d experiments, want %d", len(exp.Experiments), len(c.wantProviders))
			}
			hosts := map[string]bool{}
			for _, e := range exp.Experiments {
				hosts[e.Match.Host] = true
			}
			for _, h := range c.wantProviders {
				if !hosts[h] {
					t.Errorf("missing provider host %q (got %v)", h, hosts)
				}
			}
		})
	}
}
