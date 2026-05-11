package proxy_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// e2e wires the standard end-to-end proxy harness: a TLS upstream
// running handler, an Injector configured with s, martian taught to
// trust the upstream's cert, and an HTTP client routed through the
// proxy that trusts the per-run CA. Returns the upstream and client.
func e2e(t *testing.T, s *scenario.Scenario, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)

	inj, env := startInjector(t, s)

	upstreamCAs := x509.NewCertPool()
	upstreamCAs.AddCert(upstream.Certificate())
	inj.Proxy().SetRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamCAs, MinVersion: tls.VersionTLS12},
	})

	return upstream, clientThroughProxy(t, env)
}

func TestE2E_LLMAPIDegraded(t *testing.T) {
	s := &scenario.Scenario{
		Name: "test-llm-api-degraded",
		Experiments: []scenario.Experiment{{
			Name: "openai-rate-limited",
			Fault: faulttypes.Fault{
				HTTPStatus:      429,
				ResponseHeaders: map[string]string{"Retry-After": "30"},
			},
			Match:       scenario.Match{Host: "127.0.0.1"},
			Probability: 1.0,
		}},
	}
	upstream, client := e2e(t, s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("upstream should not be reached when fault fires")
		w.WriteHeader(200)
	}))

	resp, err := client.Get(upstream.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
	if got := resp.Header.Get("X-Faultkit-Synthetic"); got != "true" {
		t.Errorf("X-Faultkit-Synthetic = %q, want true", got)
	}
	// Upstream is 127.0.0.1, not api.openai.com, so we get the generic shape.
	if !bytes.Contains(body, []byte(`"error"`)) {
		t.Errorf("body missing 'error' field: %s", body)
	}
}

func TestE2E_MalformedJSON(t *testing.T) {
	const broken = `{"choices":[{"message":{"content":"{\"action\":\"x\",}"}}]}`
	s := &scenario.Scenario{
		Name: "test-malformed-json",
		Experiments: []scenario.Experiment{{
			Name: "trailing-comma",
			Fault: faulttypes.Fault{
				HTTPStatus:   200,
				ResponseBody: broken,
			},
			Match:       scenario.Match{Host: "127.0.0.1"},
			Probability: 1.0,
		}},
	}
	upstream, client := e2e(t, s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("upstream should not be reached when fault fires")
	}))

	resp, err := client.Get(upstream.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if string(body) != broken {
		t.Errorf("body = %q, want %q", body, broken)
	}
}

func TestE2E_StreamingCutoff(t *testing.T) {
	s := &scenario.Scenario{
		Name: "test-streaming-cutoff",
		Experiments: []scenario.Experiment{{
			Name:        "cut",
			Fault:       faulttypes.Fault{StreamCutoffTokens: 3},
			Match:       scenario.Match{Host: "127.0.0.1"},
			Probability: 1.0,
		}},
	}

	const upstreamEvents = 10
	upstream, client := e2e(t, s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < upstreamEvents; i++ {
			fmt.Fprintf(w, "data: {\"index\":%d}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))

	resp, err := client.Get(upstream.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	count := bytes.Count(body, []byte("data:"))
	if count != 3 {
		t.Errorf("got %d data: events, want 3 (body=%q)", count, body)
	}
	if bytes.Contains(body, []byte("[DONE]")) {
		t.Errorf("body should not contain [DONE]: %s", body)
	}
}

func TestE2E_PassthroughWhenProbabilityZero(t *testing.T) {
	const want = "real upstream body"
	s := &scenario.Scenario{
		Name: "test-passthrough",
		Experiments: []scenario.Experiment{{
			Name:        "would-fire",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "127.0.0.1"},
			Probability: 0,
		}},
	}
	upstream, client := e2e(t, s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(want))
	}))

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if resp.Header.Get("X-Faultkit-Synthetic") != "" {
		t.Error("X-Faultkit-Synthetic should not be set on passthrough")
	}
}
