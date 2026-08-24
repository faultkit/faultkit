package fixtures_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

func TestBedrockErrorEnvelope(t *testing.T) {
	syn := fixtures.Build("bedrock-runtime.us-east-1.amazonaws.com",
		faulttypes.Fault{HTTPStatus: 429})
	if syn.Status != 429 {
		t.Fatalf("status = %d, want 429", syn.Status)
	}
	// AWS JSON error: body is {"message": "..."} — no OpenAI/Anthropic keys.
	if !bytes.Contains(syn.Body, []byte(`"message"`)) {
		t.Errorf("body = %s, want a \"message\" field", syn.Body)
	}
	// Bedrock uses a FLAT AWS-JSON error `{"message":"..."}` — NOT the nested
	// `{"error":{...}}` wrapper that openai/anthropic AND the generic fallback
	// all use. This negative assertion is what makes the test genuinely RED on
	// current code (bedrock host → generic → has `"error"`) and GREEN once the
	// bedrock template is added.
	if bytes.Contains(syn.Body, []byte(`"error"`)) {
		t.Errorf("body = %s, want a flat {\"message\":...} error, not an {\"error\":{...}} wrapper", syn.Body)
	}
}

func TestBedrockConverseFixtures(t *testing.T) {
	cases := []struct{ mode, wantSub string }{
		{"max-tokens-truncation", `"stopReason":"max_tokens"`},
		{"malformed-tool-use", `"toolUse"`},
		{"malformed-json", `\"args\":{\"id\":42},}`},
	}
	for _, c := range cases {
		f, ok := fixtures.For(c.mode, "bedrock")
		if !ok {
			t.Fatalf("no bedrock fixture for %q", c.mode)
		}
		if !strings.Contains(f.Body, c.wantSub) {
			t.Errorf("%s bedrock body = %s, want substring %q", c.mode, f.Body, c.wantSub)
		}
		if f.Path != "/model/*/converse" {
			t.Errorf("%s bedrock path = %q, want /model/*/converse", c.mode, f.Path)
		}
	}
}

func TestBedrockOnlyModesAreSingleProvider(t *testing.T) {
	for _, mode := range []string{"model-timeout", "service-unavailable"} {
		if _, ok := fixtures.For(mode, "openai"); ok {
			t.Errorf("%q must not have an openai fixture", mode)
		}
		if _, ok := fixtures.For(mode, "anthropic"); ok {
			t.Errorf("%q must not have an anthropic fixture", mode)
		}
		if _, ok := fixtures.For(mode, "bedrock"); !ok {
			t.Errorf("%q must have a bedrock fixture", mode)
		}
	}
}
