package proxy_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// End-to-end for base-URL mode: Start returns a provider base-URL env
// pointing at faultkit's origin server, and a client hitting that base URL
// gets the synthesized fault — with no HTTPS_PROXY and no CA involved.
func TestInjectorBaseURLMode(t *testing.T) {
	s := &scenario.Scenario{
		Name: "baseurl-429",
		Experiments: []scenario.Experiment{{
			Name:        "openai-429",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*"},
			Probability: 1,
		}},
	}

	inj := proxy.New()
	inj.UseBaseURL(true)
	env, err := inj.Start(context.Background(), s)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = inj.Stop(context.Background()) })

	base := envValue(env, "OPENAI_BASE_URL")
	if !strings.HasPrefix(base, "http://127.0.0.1:") || !strings.HasSuffix(base, "/__fk/openai") {
		t.Fatalf("OPENAI_BASE_URL = %q, want http://127.0.0.1:PORT/__fk/openai", base)
	}
	if envValue(env, "OPENAI_API_BASE") != base {
		t.Errorf("OPENAI_API_BASE not set to the same base URL")
	}

	resp, err := http.Get(base + "/v1/chat/completions") //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET base URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != 429 {
		t.Errorf("status = %d, want 429 (synthetic fault)", resp.StatusCode)
	}
	if resp.Header.Get("X-Faultkit-Synthetic") != "true" {
		t.Errorf("response was not synthesized by faultkit (missing X-Faultkit-Synthetic)")
	}
}

// A scenario whose hosts aren't known providers can't be served in
// base-URL mode; Start must fail loudly rather than silently no-op.
func TestInjectorBaseURLModeUnknownHost(t *testing.T) {
	s := &scenario.Scenario{
		Name: "baseurl-unknown",
		Experiments: []scenario.Experiment{{
			Name:        "x",
			Fault:       faulttypes.Fault{HTTPStatus: 500},
			Match:       scenario.Match{Host: "example.com"},
			Probability: 1,
		}},
	}
	inj := proxy.New()
	inj.UseBaseURL(true)
	if _, err := inj.Start(context.Background(), s); err == nil {
		t.Error("Start should fail when no scenario host maps to a known provider")
		_ = inj.Stop(context.Background())
	}
}
