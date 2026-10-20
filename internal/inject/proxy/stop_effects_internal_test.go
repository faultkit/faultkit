package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

// White-box: the tests wait on the faulter's stop channel, which
// Injector.Stop closes before anything else, so they release the upstream
// only once Stop has begun.

// gatedUpstream flushes the response headers and head as soon as a request
// arrives, then waits for release before it writes tail.
type gatedUpstream struct {
	contentType string
	head, tail  string

	arrived chan struct{}
	release chan struct{}
}

func newGatedUpstream(contentType, head, tail string) *gatedUpstream {
	return &gatedUpstream{
		contentType: contentType,
		head:        head,
		tail:        tail,
		arrived:     make(chan struct{}, 1),
		release:     make(chan struct{}),
	}
}

func (u *gatedUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", u.contentType)
	_, _ = io.WriteString(w, u.head)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	u.arrived <- struct{}{}
	<-u.release
	_, _ = io.WriteString(w, u.tail)
}

// startForwardInjector starts a forward-proxy injector for s in front of a
// TLS upstream running handler. It returns the injector, the upstream and
// a client that sends its requests through the proxy and trusts its CA.
func startForwardInjector(t *testing.T, s *scenario.Scenario, handler http.Handler) (*Injector, *httptest.Server, *http.Client) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)

	inj := startSealTestInjector(t, s, false)
	upstreamCAs := x509.NewCertPool()
	upstreamCAs.AddCert(upstream.Certificate())
	inj.Proxy().SetRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamCAs, MinVersion: tls.VersionTLS12},
	})

	proxyCAs := x509.NewCertPool()
	proxyCAs.AddCert(inj.ca.Cert())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: inj.server.ln.Addr().String()}),
			TLSClientConfig: &tls.Config{RootCAs: proxyCAs, MinVersion: tls.VersionTLS12},
		},
		Timeout: 10 * time.Second,
	}
	return inj, upstream, client
}

// await waits for ch, failing the test after 5 seconds.
func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// A response mutation or a stream cut applied after Stop began doesn't count
// as fired. The CLI calls Stop once the target has exited, so the target
// never saw that effect, yet martian's Close waits for the connection to
// finish, and the effect used to emit before the channel was sealed.
func TestEffectAfterStopBeganNotFired(t *testing.T) {
	const cutAt = 3
	toolResult := `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"n":1}}}`
	mutated := `{"id":1,"jsonrpc":"2.0","result":{"structuredContent":{"n":"1"}}}`
	cases := []struct {
		name     string
		fault    faulttypes.Fault
		upstream *gatedUpstream
		wantBody string // empty: the stream must be cut after cutAt deltas
	}{
		{
			name:     "JSON tool result mutated",
			fault:    faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"},
			upstream: newGatedUpstream("application/json", "", toolResult),
			wantBody: mutated,
		},
		{
			name:     "SSE tool result mutated",
			fault:    faulttypes.Fault{ResponseMutation: "mcp-schema-mismatch"},
			upstream: newGatedUpstream(sseContentType, ": ping\n\n", "data: "+toolResult+"\n\n"),
			wantBody: ": ping\n\ndata: " + mutated + "\n\n",
		},
		{
			name:     "stream cut",
			fault:    faulttypes.Fault{StreamCutoffTokens: cutAt},
			upstream: newGatedUpstream(sseContentType, strings.TrimSuffix(openAIDeltas(cutAt-1), sseDone), openAIDeltas(cutAt)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &scenario.Scenario{
				Name: "effect-after-stop",
				Experiments: []scenario.Experiment{{
					Name:        "effect",
					Fault:       tc.fault,
					Match:       scenario.Match{Host: "127.0.0.1"},
					Probability: 1,
				}},
			}
			inj, upstream, client := startForwardInjector(t, s, tc.upstream)

			bodies := make(chan string, 1)
			go func() {
				resp, err := client.Post(upstream.URL+"/v1/mcp", "application/json",
					strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
				if err != nil {
					bodies <- "error: " + err.Error()
					return
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					bodies <- "error: " + err.Error()
					return
				}
				bodies <- string(b)
			}()

			await(t, tc.upstream.arrived, "the request to reach the upstream")
			stopped := make(chan error, 1)
			go func() { stopped <- inj.Stop(context.Background()) }()
			await(t, inj.faulter.stop, "Stop to begin")
			close(tc.upstream.release)

			body := await(t, bodies, "the client's response")
			if err := await(t, stopped, "Stop to return"); err != nil {
				t.Fatalf("Stop: %v", err)
			}

			// The effect itself was still applied; only its event is gone.
			switch {
			case tc.wantBody != "" && body != tc.wantBody:
				t.Errorf("client got %q, want the effect applied: %q", body, tc.wantBody)
			case tc.wantBody == "" && (strings.Count(body, "data:") != cutAt || strings.Contains(body, "[DONE]")):
				t.Errorf("client got %q, want the stream cut after %d deltas", body, cutAt)
			}
			if got := drainEvents(t, inj.Events()); len(got) != 0 {
				t.Errorf("events = %+v, want none for an effect applied after Stop began", got)
			}
		})
	}
}
