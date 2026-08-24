package fixtures_test

import (
	"bytes"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
)

func TestBuildDispatchByHost(t *testing.T) {
	cases := []struct {
		host    string
		wantSub string // a substring unique to that vendor's error envelope
	}{
		{"api.openai.com", `"rate_limit_error"`},
		{"api.anthropic.com", `"type":"error"`},
		{"unknown.example.com", `"api_error"`},
	}
	for _, c := range cases {
		syn := fixtures.Build(c.host, faulttypes.Fault{HTTPStatus: 429})
		if !bytes.Contains(syn.Body, []byte(c.wantSub)) {
			t.Errorf("Build(%q) body = %s, want substring %q", c.host, syn.Body, c.wantSub)
		}
	}
}
