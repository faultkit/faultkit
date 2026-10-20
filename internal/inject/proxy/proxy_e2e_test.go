package proxy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// e2e wires the standard end-to-end proxy harness: a TLS upstream
// running handler, an Injector configured with s, martian taught to
// trust the upstream's cert, and an HTTP client routed through the
// proxy that trusts the per-run CA. Returns the upstream and client.
func e2e(t *testing.T, s *scenario.Scenario, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	_, upstream, client := e2eInjector(t, s, handler)
	return upstream, client
}

// e2eInjector is e2e that also returns the injector, for tests that check
// the events it emitted.
func e2eInjector(t *testing.T, s *scenario.Scenario, handler http.Handler) (*proxy.Injector, *httptest.Server, *http.Client) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)

	inj, env := startInjector(t, s)

	upstreamCAs := x509.NewCertPool()
	upstreamCAs.AddCert(upstream.Certificate())
	inj.Proxy().SetRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamCAs, MinVersion: tls.VersionTLS12},
	})

	return inj, upstream, clientThroughProxy(t, env)
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
	upstream, client := e2e(t, s, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
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

	// Upstream emits Anthropic-shape content_block_delta events (the shape
	// isContentDelta recognises) so cut-at-3 lands after the 3rd content delta.
	const upstreamEvents = 10
	upstream, client := e2e(t, s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < upstreamEvents; i++ {
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":%d}\n\n", i)
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

// openAIStream returns prefix, then deltas OpenAI-shape content deltas,
// then the [DONE] event when done is set.
func openAIStream(prefix string, deltas int, done bool) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := range deltas {
		fmt.Fprintf(&b, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
	}
	if done {
		b.WriteString("data: [DONE]\n\n")
	}
	return b.String()
}

// oversizedDelta is a content-delta event whose line is longer than the
// 1 MiB faultkit classifies, so it must pass through without counting.
var oversizedDelta = `data: {"choices":[{"delta":{"content":"` + strings.Repeat("x", 1<<20) + `"}}]}` + "\n\n"

// sseUpstream serves body as an SSE stream. It flushes the headers first so
// the body is chunked, as provider streams are. With abort set it then drops
// the connection, so the proxy's upstream read fails mid-stream.
func sseUpstream(body string, abort bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		if f != nil {
			f.Flush()
		}
		_, _ = io.WriteString(w, body)
		if abort {
			if f != nil {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}
	})
}

// A stream-cutoff fault fires only when the stream is actually cut. A
// non-SSE response or a stream shorter than the cut point passes through
// whole and unfired, a line too long to classify passes through without
// counting, and an upstream error before the cut is a non-fired error event.
func TestE2E_StreamingCutoffFiresOnlyWhenCut(t *testing.T) {
	const cutAt = 3
	short := openAIStream("", cutAt-1, true)
	oversizedShort := openAIStream(oversizedDelta, cutAt-1, true)
	cases := []struct {
		name      string
		handler   http.Handler
		wantBody  string // when set, the client must get exactly this
		wantFired int
		wantErrs  int
	}{
		{name: "non-SSE response", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[]}`))
		})},
		{name: "stream shorter than the cut point", handler: sseUpstream(short, false), wantBody: short},
		{name: "stream reaches the cut point", handler: sseUpstream(openAIStream("", cutAt, true), false), wantFired: 1},
		{name: "stream longer than the cut point", handler: sseUpstream(openAIStream("", cutAt+5, true), false), wantFired: 1},
		{name: "oversized line before too few deltas", handler: sseUpstream(oversizedShort, false), wantBody: oversizedShort},
		{name: "oversized line then enough deltas", handler: sseUpstream(openAIStream(oversizedDelta, cutAt, true), false), wantFired: 1},
		{name: "upstream error before the cut point", handler: sseUpstream(openAIStream("", cutAt-1, false), true), wantErrs: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &scenario.Scenario{
				Name: "test-streaming-cutoff-fired",
				Experiments: []scenario.Experiment{{
					Name:        "cut",
					Fault:       faulttypes.Fault{StreamCutoffTokens: cutAt},
					Match:       scenario.Match{Host: "127.0.0.1"},
					Probability: 1.0,
				}},
			}
			inj, upstream, client := e2eInjector(t, s, tc.handler)

			resp, err := client.Get(upstream.URL + "/v1/chat/completions")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if tc.wantBody != "" && string(body) != tc.wantBody {
				t.Errorf("client got %d bytes, want the %d upstream bytes unchanged", len(body), len(tc.wantBody))
			}

			// The cut and error events are sent before the client sees the
			// end of the body, so after Stop the channel holds all of them.
			if err := inj.Stop(context.Background()); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			assertCutoffEvents(t, inj.Events(), tc.wantFired, tc.wantErrs)
		})
	}
}

// assertCutoffEvents drains events and checks it held wantFired fired
// events and wantErrs non-fired error events, and nothing else.
func assertCutoffEvents(t *testing.T, events <-chan inject.Event, wantFired, wantErrs int) {
	t.Helper()
	var fired, errs int
	for ev := range events {
		switch {
		case ev.Fired && ev.Err == "":
			fired++
		case !ev.Fired && ev.Err != "":
			errs++
		default:
			t.Errorf("unexpected event %+v", ev)
		}
	}
	if fired != wantFired || errs != wantErrs {
		t.Errorf("fired events = %d, error events = %d; want %d and %d", fired, errs, wantFired, wantErrs)
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
