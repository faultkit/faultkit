package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: origin mode reuses unexported faulter/registry internals.

func newOriginTestHandler(t *testing.T, s *scenario.Scenario) (*originHandler, chan inject.Event) {
	t.Helper()
	events := make(chan inject.Event, 8)
	return newOriginHandler(NewFaulter(s, events, nil), nil), events
}

// A 100%-probability HTTP fault on the upstream host is synthesized
// directly, without any upstream round trip.
func TestOriginSyntheticFault(t *testing.T) {
	s := &scenario.Scenario{
		Name: "origin-429",
		Experiments: []scenario.Experiment{{
			Name:        "openai-429",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*"},
			Probability: 1,
		}},
	}
	h, events := newOriginTestHandler(t, s)

	req := httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get(syntheticHeader) != "true" {
		t.Errorf("missing %s header; response was not synthesized by faultkit", syntheticHeader)
	}

	select {
	case ev := <-events:
		if !ev.Fired || ev.Host != "api.openai.com" {
			t.Errorf("event = %+v, want fired on api.openai.com", ev)
		}
	default:
		t.Error("expected a fired fault event")
	}
}

// A path with no provider prefix is rejected (it is not API traffic we
// know how to route in origin mode).
func TestOriginUnknownPath(t *testing.T) {
	h, _ := newOriginTestHandler(t, &scenario.Scenario{Name: "empty"})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unrecognized base-URL path", rec.Code)
	}
}

// Anthropic /v1/messages routes through the anthropic prefix and fires its
// experiment — the parity added for Claude-only agentic tools (T11).
func TestOriginAnthropicSynthetic(t *testing.T) {
	body := `{"id":"msg_test","type":"message","content":[{"type":"text","text":"x"}],}`
	s := &scenario.Scenario{
		Name: "anthropic-malformed",
		Experiments: []scenario.Experiment{{
			Name:        "anthropic-malformed",
			Fault:       faulttypes.Fault{HTTPStatus: 200, ResponseBody: body},
			Match:       scenario.Match{Host: "api.anthropic.com", Path: "/v1/messages"},
			Probability: 1,
		}},
	}
	h, events := newOriginTestHandler(t, s)

	req := httptest.NewRequest(http.MethodPost, "/__fk/anthropic/v1/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want the synthetic malformed body", rec.Body.String())
	}
	select {
	case ev := <-events:
		if !ev.Fired || ev.Host != "api.anthropic.com" {
			t.Errorf("event = %+v, want fired on api.anthropic.com", ev)
		}
	default:
		t.Error("expected a fired fault event")
	}
}

// routeToLocalUpstream points h's reverse proxy at a local TLS server
// running handler, whatever upstream host the provider names.
func routeToLocalUpstream(t *testing.T, h *originHandler, handler http.Handler) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	addr := upstream.Listener.Addr().String()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		// httptest's certificate names example.com, not the provider host.
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.com", MinVersion: tls.VersionTLS12},
	}
	t.Cleanup(tr.CloseIdleConnections)
	h.rp.Transport = tr
}

// originSSEUpstream serves body as an SSE stream. With abort set it then
// drops the connection, so the upstream read fails mid-stream.
func originSSEUpstream(body string, abort bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", sseContentType)
		_, _ = io.WriteString(w, body)
		if abort {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}
	})
}

// Base-URL mode: a stream-cutoff fault fires only when the stream is
// actually cut. A non-SSE response or a stream shorter than the cut point
// passes through whole and unfired, a line too long to classify passes
// through without counting, and an upstream error before the cut is a
// non-fired error event.
func TestOriginStreamingCutoffFiresOnlyWhenCut(t *testing.T) {
	const cutAt = 3
	short := openAIDeltas(cutAt - 1)
	oversized := deltaLine(maxClassifiedLine+1) + "\n"
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
		{name: "stream shorter than the cut point", handler: originSSEUpstream(short, false), wantBody: short},
		{name: "stream reaches the cut point", handler: originSSEUpstream(openAIDeltas(cutAt), false), wantFired: 1},
		{name: "stream longer than the cut point", handler: originSSEUpstream(openAIDeltas(cutAt+5), false), wantFired: 1},
		{name: "oversized line before too few deltas", handler: originSSEUpstream(oversized+short, false), wantBody: oversized + short},
		{name: "oversized line then enough deltas", handler: originSSEUpstream(oversized+openAIDeltas(cutAt), false), wantFired: 1},
		{name: "upstream error before the cut point", handler: originSSEUpstream(strings.TrimSuffix(short, sseDone), true), wantErrs: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &scenario.Scenario{
				Name: "origin-cutoff",
				Experiments: []scenario.Experiment{{
					Name:        "openai-cutoff",
					Fault:       faulttypes.Fault{StreamCutoffTokens: cutAt},
					Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*"},
					Probability: 1,
				}},
			}
			h, events := newOriginTestHandler(t, s)
			routeToLocalUpstream(t, h, tc.handler)

			// ServeHTTP returns after the reverse proxy copied the whole
			// body, and the cut and error events are sent before the body
			// ends, so the channel already holds all of them.
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want the upstream's 200", rec.Code)
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("client got %d bytes, want the %d upstream bytes unchanged", rec.Body.Len(), len(tc.wantBody))
			}

			var fired, errs int
			for len(events) > 0 {
				switch ev := <-events; {
				case ev.Fired && ev.Err == "":
					fired++
				case !ev.Fired && ev.Err != "":
					errs++
				default:
					t.Errorf("unexpected event %+v", ev)
				}
			}
			if fired != tc.wantFired || errs != tc.wantErrs {
				t.Errorf("fired events = %d, error events = %d; want %d and %d", fired, errs, tc.wantFired, tc.wantErrs)
			}
		})
	}
}

// Base-URL mode honours match.method: a POST the method matches gets the
// synthetic fault without an upstream round trip, while a GET under the same
// host and path glob is forwarded to the upstream untouched.
func TestOriginMatchMethod(t *testing.T) {
	const upstreamBody = `{"object":"list","data":[]}`
	s := &scenario.Scenario{
		Name: "origin-method",
		Experiments: []scenario.Experiment{{
			Name:        "openai-post-429",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*", Method: "post"},
			Probability: 1,
		}},
	}
	h, events := newOriginTestHandler(t, s)
	var (
		mu      sync.Mutex
		reached []string
	)
	routeToLocalUpstream(t, h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached = append(reached, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, upstreamBody)
	}))

	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/__fk/openai/v1/models", nil))
	if get.Code != http.StatusOK || get.Body.String() != upstreamBody || get.Header().Get(syntheticHeader) != "" {
		t.Errorf("GET: got %d %q (synthetic %q), want the upstream's 200 %q", get.Code, get.Body.String(), get.Header().Get(syntheticHeader), upstreamBody)
	}

	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions", strings.NewReader(`{"model":"x"}`)))
	if post.Code != http.StatusTooManyRequests || post.Header().Get(syntheticHeader) != "true" {
		t.Errorf("POST: got %d (synthetic %q), want a synthetic 429", post.Code, post.Header().Get(syntheticHeader))
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"GET /v1/models"}; !slices.Equal(reached, want) {
		t.Errorf("upstream saw %v, want only %v", reached, want)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (the POST)", len(events))
	}
	if ev := <-events; !ev.Fired || ev.Experiment != "openai-post-429" {
		t.Errorf("event = %+v, want the POST's fired event", ev)
	}
}

// Base-URL mode can't route MCP hosts, so a response mutation is
// forward-proxy only. Here a matched request is forwarded, never
// synthesized, and even a tools/call response with structuredContent
// passes through unmutated, with no event.
func TestOriginResponseMutationPassesThrough(t *testing.T) {
	const upstreamBody = `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"n":1}}}`
	s := &scenario.Scenario{
		Name: "origin-mutation",
		Experiments: []scenario.Experiment{{
			Name:        "openai-mutation",
			Fault:       faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"},
			Match:       scenario.Match{Host: "api.openai.com", Path: "/v1/*"},
			Probability: 1,
		}},
	}
	h, events := newOriginTestHandler(t, s)
	up := &upstreamRecorder{next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, upstreamBody)
	})}
	routeToLocalUpstream(t, h, up)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__fk/openai/v1/chat/completions",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)))
	if rec.Code != http.StatusOK || rec.Body.String() != upstreamBody || rec.Header().Get(syntheticHeader) != "" {
		t.Errorf("got %d %q (synthetic %q), want the upstream's 200 %q", rec.Code, rec.Body.String(), rec.Header().Get(syntheticHeader), upstreamBody)
	}
	up.mu.Lock()
	hits := up.hits
	up.mu.Unlock()
	if hits != 1 {
		t.Errorf("upstream reached %d times, want 1", hits)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want none", len(events))
	}
}
