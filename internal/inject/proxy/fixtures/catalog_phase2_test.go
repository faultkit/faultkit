package fixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
)

func TestCatalogPhase2(t *testing.T) {
	t.Run("overloaded is anthropic-only", func(t *testing.T) {
		f, ok := fixtures.For("overloaded", "anthropic")
		if !ok {
			t.Fatal("expected (overloaded, anthropic)")
		}
		if f.Status != 529 {
			t.Errorf("Status = %d, want 529", f.Status)
		}
		if _, ok := fixtures.For("overloaded", "openai"); ok {
			t.Error("overloaded should NOT exist for openai (529 is Anthropic-specific)")
		}
	})

	t.Run("max-tokens-truncation per provider", func(t *testing.T) {
		oa, ok := fixtures.For("max-tokens-truncation", "openai")
		if !ok {
			t.Fatal("expected (max-tokens-truncation, openai)")
		}
		if !strings.Contains(oa.Body, `"finish_reason":"length"`) {
			t.Errorf("openai body should carry finish_reason length: %s", oa.Body)
		}
		an, ok := fixtures.For("max-tokens-truncation", "anthropic")
		if !ok {
			t.Fatal("expected (max-tokens-truncation, anthropic)")
		}
		if !strings.Contains(an.Body, `"stop_reason":"max_tokens"`) {
			t.Errorf("anthropic body should carry stop_reason max_tokens: %s", an.Body)
		}
	})

	t.Run("malformed-tool-use per provider", func(t *testing.T) {
		oa, ok := fixtures.For("malformed-tool-use", "openai")
		if !ok {
			t.Fatal("expected (malformed-tool-use, openai)")
		}
		// OpenAI tool args are a JSON string; the malformed case makes that
		// string itself invalid JSON.
		var env struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct{ Arguments string }
					} `json:"tool_calls"`
				}
			}
		}
		if err := json.Unmarshal([]byte(oa.Body), &env); err != nil {
			t.Fatalf("openai envelope must be valid JSON: %v", err)
		}
		args := env.Choices[0].Message.ToolCalls[0].Function.Arguments
		if json.Valid([]byte(args)) {
			t.Errorf("openai tool arguments should be MALFORMED JSON, got valid: %q", args)
		}
		if _, ok := fixtures.For("malformed-tool-use", "anthropic"); !ok {
			t.Fatal("expected (malformed-tool-use, anthropic)")
		}
	})
}
