package proxy_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// A target that reaches the proxy but can't finish the MITM TLS handshake
// never sends a request, so RequestsSeen stays 0. FailedHandshakes must count
// it, or the CLI reports the run as "no requests reached faultkit" and sends
// the user looking for a proxy setting the client already honours.
func TestFailedHandshakesCounted(t *testing.T) {
	const target = "api.example.test:443"
	tests := []struct {
		name string
		cfg  func(injectedCA *x509.CertPool) *tls.Config
	}{
		{"client doesn't trust the per-run CA", func(*x509.CertPool) *tls.Config {
			return &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "api.example.test", MinVersion: tls.VersionTLS12}
		}},
		{"client offers only HTTP/2, like gRPC", func(injectedCA *x509.CertPool) *tls.Config {
			return &tls.Config{RootCAs: injectedCA, ServerName: "api.example.test", NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inj, env := startInjector(t, nil)
			conn := connectThroughProxy(t, env, target)
			if err := tls.Client(conn, tt.cfg(injectedCAPool(t, env))).Handshake(); err == nil {
				t.Fatal("handshake succeeded; want it to fail")
			}

			// The proxy records the failure on its own goroutine, after the
			// client has already seen the alert.
			n, last := inj.FailedHandshakes()
			for deadline := time.Now().Add(2 * time.Second); n == 0 && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
				n, last = inj.FailedHandshakes()
			}
			if n != 1 || !strings.HasPrefix(last, target+": ") {
				t.Errorf("FailedHandshakes() = %d, %q; want 1 failure for %s", n, last, target)
			}
			if got := inj.RequestsSeen(); got != 0 {
				t.Errorf("RequestsSeen() = %d; want 0, no request got past the handshake", got)
			}
		})
	}
}

// connectThroughProxy opens a CONNECT tunnel to target through the proxy in
// env and returns the raw connection, ready for the client's TLS handshake.
func connectThroughProxy(t *testing.T, env []string, target string) net.Conn {
	t.Helper()
	proxyURL, err := url.Parse(envValue(env, "HTTPS_PROXY"))
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := conn.Write([]byte("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n\r\n")); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	// The proxy waits for the client hello after its 200, so the reader can't
	// buffer past the response.
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", res.StatusCode)
	}
	return conn
}

func injectedCAPool(t *testing.T, env []string) *x509.CertPool {
	t.Helper()
	pem, err := os.ReadFile(envValue(env, "SSL_CERT_FILE"))
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("CA PEM did not append")
	}
	return pool
}
