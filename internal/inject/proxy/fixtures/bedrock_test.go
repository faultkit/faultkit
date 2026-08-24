package fixtures_test

import (
	"bytes"
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
