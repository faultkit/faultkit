package proxy_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/faultkit-dev/faultkit/internal/inject/proxy"
	"github.com/faultkit-dev/faultkit/pkg/faulttypes"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

func newTestScenario() *scenario.Scenario {
	return &scenario.Scenario{
		Name: "round-trip-test",
		Experiments: []scenario.Experiment{{
			Name:        "noop",
			Fault:       faulttypes.Fault{HTTPStatus: 429},
			Match:       scenario.Match{Host: "example.com"},
			Probability: 0,
		}},
	}
}

func startInjector(t *testing.T, s *scenario.Scenario) (*proxy.Injector, []string) {
	t.Helper()
	if s == nil {
		s = newTestScenario()
	}
	inj := proxy.New()
	env, err := inj.Start(context.Background(), s)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := inj.Stop(context.Background()); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return inj, env
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

func clientThroughProxy(t *testing.T, env []string) *http.Client {
	t.Helper()
	proxyURLStr := envValue(env, "HTTPS_PROXY")
	caPath := envValue(env, "SSL_CERT_FILE")
	if proxyURLStr == "" || caPath == "" {
		t.Fatalf("missing env: HTTPS_PROXY=%q SSL_CERT_FILE=%q", proxyURLStr, caPath)
	}
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("CA PEM did not append")
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 10 * time.Second,
	}
}

func TestProxyRoundTrip(t *testing.T) {
	const wantBody = "hello upstream"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(wantBody))
	}))
	defer upstream.Close()

	inj, env := startInjector(t, nil)

	upstreamCAs := x509.NewCertPool()
	upstreamCAs.AddCert(upstream.Certificate())
	inj.Proxy().SetRoundTripper(&http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: upstreamCAs, MinVersion: tls.VersionTLS12},
	})

	client := clientThroughProxy(t, env)
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

func TestStopRemovesCAFile(t *testing.T) {
	inj, env := startInjector(t, nil)

	caPath := envValue(env, "SSL_CERT_FILE")
	if caPath == "" {
		t.Fatal("SSL_CERT_FILE not in env")
	}
	if _, err := os.Stat(caPath); err != nil {
		t.Fatalf("CA file should exist during run: %v", err)
	}

	if err := inj.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(caPath); !os.IsNotExist(err) {
		t.Errorf("CA file should be removed after Stop, stat err = %v", err)
	}

	if err := inj.Stop(context.Background()); err != nil {
		t.Errorf("second Stop should be no-op, got %v", err)
	}
}

func TestStartTwiceFails(t *testing.T) {
	inj, _ := startInjector(t, nil)
	if _, err := inj.Start(context.Background(), newTestScenario()); err == nil {
		t.Error("second Start should error")
	}
}
