package fixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/faultkit-dev/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
)

func TestBuildOpenAI429(t *testing.T) {
	f := faulttypes.Fault{HTTPStatus: 429, ResponseHeaders: map[string]string{"Retry-After": "30"}}
	got := fixtures.Build("api.openai.com", f)

	if got.Status != 429 {
		t.Errorf("Status = %d, want 429", got.Status)
	}
	if got.Headers["Retry-After"] != "30" {
		t.Errorf("Retry-After header missing or wrong: %q", got.Headers["Retry-After"])
	}
	if got.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header missing or wrong: %q", got.Headers["Content-Type"])
	}

	var parsed struct {
		Error struct {
			Message, Type, Code string
		}
	}
	if err := json.Unmarshal(got.Body, &parsed); err != nil {
		t.Fatalf("body is not valid OpenAI shape: %v\nbody=%s", err, got.Body)
	}
	if parsed.Error.Type != "rate_limit_error" {
		t.Errorf("error.type = %q, want rate_limit_error", parsed.Error.Type)
	}
	if parsed.Error.Code != "rate_limit_exceeded" {
		t.Errorf("error.code = %q, want rate_limit_exceeded", parsed.Error.Code)
	}
	if !strings.Contains(parsed.Error.Message, "Rate limit") {
		t.Errorf("error.message = %q, want substring 'Rate limit'", parsed.Error.Message)
	}
}

func TestBuildResponseBodyVerbatim(t *testing.T) {
	body := `{"choices":[{"message":{"content":"{\"action\":\"x\",}"}}]}`
	f := faulttypes.Fault{HTTPStatus: 200, ResponseBody: body}
	got := fixtures.Build("api.openai.com", f)
	if string(got.Body) != body {
		t.Errorf("Body should be verbatim; got %q want %q", got.Body, body)
	}
	if got.Status != 200 {
		t.Errorf("Status = %d, want 200", got.Status)
	}
}

func TestBuildGenericFallback(t *testing.T) {
	f := faulttypes.Fault{HTTPStatus: 503}
	got := fixtures.Build("api.unknown-vendor.com", f)
	if got.Status != 503 {
		t.Errorf("Status = %d, want 503", got.Status)
	}
	if !strings.Contains(string(got.Body), `"error"`) {
		t.Errorf("generic 5xx body should contain 'error': %s", got.Body)
	}
}
