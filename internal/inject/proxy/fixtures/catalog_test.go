package fixtures_test

import (
	"encoding/json"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
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
