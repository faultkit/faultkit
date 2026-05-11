package proxy_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

func mustReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return &http.Request{Method: http.MethodGet, URL: u, Host: u.Host}
}

func TestMatcher(t *testing.T) {
	s := &scenario.Scenario{
		Name: "matcher-test",
		Experiments: []scenario.Experiment{
			{
				Name:        "exact",
				Fault:       faulttypes.Fault{HTTPStatus: 429},
				Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/chat/completions"},
				Probability: 1,
			},
			{
				Name:        "wildcard-host",
				Fault:       faulttypes.Fault{HTTPStatus: 503},
				Match:       scenario.Match{Host: "*.example.com"},
				Probability: 1,
			},
			{
				Name:        "wildcard-path",
				Fault:       faulttypes.Fault{HTTPStatus: 502},
				Match:       scenario.Match{Host: "wild.test", Path: "/v1/*"},
				Probability: 1,
			},
			{
				Name:        "syscall-only-ignored",
				Fault:       faulttypes.Fault{Errno: "ECONNRESET"},
				Match:       scenario.Match{Syscall: "recvmsg"},
				Probability: 1,
			},
		},
	}
	m := proxy.NewMatcher(s)

	cases := []struct {
		name    string
		url     string
		wantExp string
	}{
		{"exact match", "https://api.openai.com/v1/chat/completions", "exact"},
		{"exact wrong path", "https://api.openai.com/v1/models", ""},
		{"wildcard match", "https://api.example.com/anything", "wildcard-host"},
		{"case-insensitive host", "https://API.OpenAI.com/v1/chat/completions", "exact"},
		{"host with port", "https://api.openai.com:443/v1/chat/completions", "exact"},
		{"path glob crosses slash", "https://wild.test/v1/chat/completions", "wildcard-path"},
		{"path glob single segment", "https://wild.test/v1/models", "wildcard-path"},
		{"path glob no match", "https://wild.test/health", ""},
		{"no match", "https://other.com/", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exp := m.Match(mustReq(t, c.url))
			if c.wantExp == "" {
				if exp != nil {
					t.Errorf("got %q, want nil", exp.Name)
				}
				return
			}
			if exp == nil || exp.Name != c.wantExp {
				t.Errorf("got %v, want %q", exp, c.wantExp)
			}
		})
	}
}

func TestMatcherEmpty(t *testing.T) {
	if got := proxy.NewMatcher(nil).Match(mustReq(t, "https://x/")); got != nil {
		t.Errorf("nil scenario should match nothing, got %v", got)
	}
	if got := proxy.NewMatcher(&scenario.Scenario{Name: "empty"}).Match(mustReq(t, "https://x/")); got != nil {
		t.Errorf("no experiments should match nothing, got %v", got)
	}
}
