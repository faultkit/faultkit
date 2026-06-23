package fixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

func TestCatalogFor(t *testing.T) {
	t.Run("rate-limited carries status and Retry-After, body synthesized later", func(t *testing.T) {
		f, ok := fixtures.For("rate-limited", "openai")
		if !ok {
			t.Fatal("expected a fixture for (rate-limited, openai)")
		}
		if f.Status != 429 {
			t.Errorf("Status = %d, want 429", f.Status)
		}
		if f.Headers["Retry-After"] != "30" {
			t.Errorf("Retry-After = %q, want 30", f.Headers["Retry-After"])
		}
		if f.Body != "" {
			t.Errorf("rate-limited body should be empty (synthesized per-provider), got %q", f.Body)
		}
	})

	t.Run("malformed-json anthropic: valid envelope, malformed inner content", func(t *testing.T) {
		f, ok := fixtures.For("malformed-json", "anthropic")
		if !ok {
			t.Fatal("expected a fixture for (malformed-json, anthropic)")
		}
		if f.Status != 200 {
			t.Errorf("Status = %d, want 200", f.Status)
		}
		if f.Path != "/v1/messages" {
			t.Errorf("Path = %q, want /v1/messages", f.Path)
		}
		var env struct {
			Content []struct{ Text string } `json:"content"`
		}
		if err := json.Unmarshal([]byte(f.Body), &env); err != nil {
			t.Fatalf("envelope must be valid JSON: %v\nbody=%s", err, f.Body)
		}
		if len(env.Content) == 0 {
			t.Fatal("expected content[]")
		}
		var inner any
		if err := json.Unmarshal([]byte(env.Content[0].Text), &inner); err == nil {
			t.Errorf("inner content should be MALFORMED JSON, but parsed: %q", env.Content[0].Text)
		}
	})

	t.Run("streaming-cutoff openai", func(t *testing.T) {
		f, ok := fixtures.For("streaming-cutoff", "openai")
		if !ok {
			t.Fatal("expected a fixture for (streaming-cutoff, openai)")
		}
		if f.StreamCutoffTokens != 80 {
			t.Errorf("StreamCutoffTokens = %d, want 80", f.StreamCutoffTokens)
		}
		if f.Path != "/v1/chat/completions" {
			t.Errorf("Path = %q, want /v1/chat/completions", f.Path)
		}
	})

	t.Run("unknown mode", func(t *testing.T) {
		if _, ok := fixtures.For("does-not-exist", "openai"); ok {
			t.Error("expected ok=false for unknown mode")
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		if _, ok := fixtures.For("rate-limited", "does-not-exist"); ok {
			t.Error("expected ok=false for unknown provider")
		}
	})
}

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

// The Anthropic-distinctive modes are anthropic-only cells.
func TestCatalogAnthropicExtras(t *testing.T) {
	for _, mode := range []string{"stream-error", "tool-use-cutoff", "refusal", "request-too-large"} {
		if _, ok := fixtures.For(mode, "openai"); ok {
			t.Errorf("%q should be Anthropic-only, but has an openai fixture", mode)
		}
		if _, ok := fixtures.For(mode, "anthropic"); !ok {
			t.Errorf("expected (%q, anthropic) fixture", mode)
		}
	}

	t.Run("stream-error is a mid-stream SSE error with no terminator", func(t *testing.T) {
		f, _ := fixtures.For("stream-error", "anthropic")
		if f.Status != 200 {
			t.Errorf("Status = %d, want 200", f.Status)
		}
		if !strings.HasPrefix(f.Headers["Content-Type"], "text/event-stream") {
			t.Errorf("Content-Type = %q, want text/event-stream", f.Headers["Content-Type"])
		}
		if !strings.Contains(f.Body, "event: error") || !strings.Contains(f.Body, "overloaded_error") {
			t.Errorf("body should contain an SSE error event:\n%s", f.Body)
		}
		if strings.Contains(f.Body, "message_stop") {
			t.Errorf("body should NOT contain the message_stop terminator:\n%s", f.Body)
		}
	})

	t.Run("tool-use-cutoff: tool_use present but stop_reason max_tokens", func(t *testing.T) {
		f, _ := fixtures.For("tool-use-cutoff", "anthropic")
		var env struct {
			Content    []struct{ Type string } `json:"content"`
			StopReason string                  `json:"stop_reason"`
		}
		if err := json.Unmarshal([]byte(f.Body), &env); err != nil {
			t.Fatalf("envelope must be valid JSON: %v", err)
		}
		if env.StopReason != "max_tokens" {
			t.Errorf("stop_reason = %q, want max_tokens", env.StopReason)
		}
		hasToolUse := false
		for _, c := range env.Content {
			if c.Type == "tool_use" {
				hasToolUse = true
			}
		}
		if !hasToolUse {
			t.Errorf("expected a tool_use content block:\n%s", f.Body)
		}
	})

	t.Run("refusal: stop_reason refusal", func(t *testing.T) {
		f, _ := fixtures.For("refusal", "anthropic")
		if !strings.Contains(f.Body, `"stop_reason":"refusal"`) {
			t.Errorf("body should carry stop_reason refusal:\n%s", f.Body)
		}
	})

	t.Run("request-too-large: 413, body synthesized", func(t *testing.T) {
		f, _ := fixtures.For("request-too-large", "anthropic")
		if f.Status != 413 {
			t.Errorf("Status = %d, want 413", f.Status)
		}
		if f.Body != "" {
			t.Errorf("body should be empty (synthesized), got %q", f.Body)
		}
		// The synthesized 413 body must be the Anthropic request_too_large shape.
		syn := fixtures.Build("api.anthropic.com", faulttypes.Fault{HTTPStatus: 413})
		if !strings.Contains(string(syn.Body), "request_too_large") {
			t.Errorf("synthesized 413 body should be request_too_large shape: %s", syn.Body)
		}
	})
}

func TestCatalogKnownMode(t *testing.T) {
	if !fixtures.KnownMode("malformed-json") {
		t.Error("malformed-json should be a known mode")
	}
	if fixtures.KnownMode("nope") {
		t.Error("nope should not be a known mode")
	}
	if len(fixtures.Modes()) == 0 {
		t.Error("Modes() should be non-empty")
	}
}
