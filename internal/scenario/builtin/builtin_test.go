package builtin_test

import (
	"slices"
	"testing"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
	"github.com/faultkit/faultkit/pkg/scenario"
)

func TestBuiltinsLoadable(t *testing.T) {
	got := scenario.BuiltinNames()
	for _, want := range []string{
		"anthropic-overloaded",
		"anthropic-refusal",
		"anthropic-request-too-large",
		"anthropic-stream-error",
		"anthropic-tool-use-cutoff",
		"flaky-network",
		"llm-api-degraded",
		"llm-streaming-cutoff",
		"malformed-json-response",
		"malformed-tool-use",
		"max-tokens-truncation",
		"tool-permission-denied",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("BuiltinNames missing %q (got %v)", want, got)
			continue
		}
		s, err := scenario.LoadBuiltin(want)
		if err != nil {
			t.Errorf("LoadBuiltin(%q): %v", want, err)
			continue
		}
		if s.Name != want {
			t.Errorf("LoadBuiltin(%q).Name = %q", want, s.Name)
		}
	}
}
