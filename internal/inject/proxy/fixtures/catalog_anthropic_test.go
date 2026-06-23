package fixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

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
