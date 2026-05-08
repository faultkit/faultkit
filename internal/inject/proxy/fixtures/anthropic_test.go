package fixtures_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/faultkit-dev/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
)

type anthropicEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestBuildAnthropic429(t *testing.T) {
	f := faulttypes.Fault{HTTPStatus: 429, ResponseHeaders: map[string]string{"Retry-After": "30"}}
	got := fixtures.Build("api.anthropic.com", f)

	if got.Status != 429 {
		t.Errorf("Status = %d, want 429", got.Status)
	}
	if got.Headers["Retry-After"] != "30" {
		t.Errorf("Retry-After header missing or wrong: %q", got.Headers["Retry-After"])
	}

	var env anthropicEnvelope
	if err := json.Unmarshal(got.Body, &env); err != nil {
		t.Fatalf("body is not valid Anthropic envelope: %v\nbody=%s", err, got.Body)
	}
	if env.Type != "error" {
		t.Errorf("envelope.type = %q, want error", env.Type)
	}
	if env.Error.Type != "rate_limit_error" {
		t.Errorf("error.type = %q, want rate_limit_error", env.Error.Type)
	}
	if !strings.Contains(env.Error.Message, "rate limit") {
		t.Errorf("error.message = %q, want substring 'rate limit'", env.Error.Message)
	}
}

func TestBuildAnthropic529Overloaded(t *testing.T) {
	got := fixtures.Build("api.anthropic.com", faulttypes.Fault{HTTPStatus: 529})

	var env anthropicEnvelope
	if err := json.Unmarshal(got.Body, &env); err != nil {
		t.Fatalf("body parse: %v\n%s", err, got.Body)
	}
	if env.Error.Type != "overloaded_error" {
		t.Errorf("error.type = %q, want overloaded_error", env.Error.Type)
	}
}

func TestBuildAnthropic503AlsoOverloaded(t *testing.T) {
	got := fixtures.Build("api.anthropic.com", faulttypes.Fault{HTTPStatus: 503})

	var env anthropicEnvelope
	if err := json.Unmarshal(got.Body, &env); err != nil {
		t.Fatalf("body parse: %v\n%s", err, got.Body)
	}
	if env.Error.Type != "overloaded_error" {
		t.Errorf("error.type = %q, want overloaded_error", env.Error.Type)
	}
}

func TestBuildAnthropicResponseBodyVerbatim(t *testing.T) {
	body := `{"type":"error","error":{"type":"custom","message":"x"}}`
	f := faulttypes.Fault{HTTPStatus: 400, ResponseBody: body}
	got := fixtures.Build("api.anthropic.com", f)
	if string(got.Body) != body {
		t.Errorf("body = %q, want verbatim %q", got.Body, body)
	}
}
