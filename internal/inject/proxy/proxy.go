package proxy

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/martian/v3"

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

const eventBuffer = 256

// proxyEnvKeys are the env vars set on the target so its HTTP/HTTPS
// traffic flows through faultkit's proxy. Both upper- and lower-case
// variants are needed in practice — Python's `requests` reads
// HTTPS_PROXY, libcurl reads CURL_CA_BUNDLE, Node reads
// NODE_EXTRA_CA_CERTS, etc.
var (
	proxyURLEnvKeys = []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"}
	caPathEnvKeys   = []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE"}
)

// Injector implements inject.Injector for the HTTPS proxy mechanism.
type Injector struct {
	ca     *CA
	server *Server
	events chan inject.Event

	stopOnce sync.Once
	stopErr  error
}

// New returns a new, unstarted Injector.
func New() *Injector {
	return &Injector{events: make(chan inject.Event, eventBuffer)}
}

// Start generates a per-run CA, starts the proxy on 127.0.0.1:0, and
// returns the env vars the runner must merge into the target's
// environment so its HTTP/HTTPS traffic flows through the proxy and
// trusts our CA.
func (i *Injector) Start(_ context.Context, _ *scenario.Scenario) ([]string, error) {
	if i.server != nil {
		return nil, errors.New("proxy: already started")
	}

	ca, err := NewCA()
	if err != nil {
		return nil, err
	}
	i.ca = ca

	pemPath, err := ca.WriteCertPEM()
	if err != nil {
		return nil, err
	}

	server, err := NewServer(ca)
	if err != nil {
		_ = ca.Cleanup()
		return nil, err
	}
	addr, err := server.Listen()
	if err != nil {
		_ = ca.Cleanup()
		return nil, err
	}
	i.server = server

	url := fmt.Sprintf("http://%s", addr)
	env := make([]string, 0, len(proxyURLEnvKeys)+len(caPathEnvKeys))
	for _, k := range proxyURLEnvKeys {
		env = append(env, k+"="+url)
	}
	for _, k := range caPathEnvKeys {
		env = append(env, k+"="+pemPath)
	}
	return env, nil
}

// Stop tears down the proxy and removes the CA temp file. Idempotent.
func (i *Injector) Stop(_ context.Context) error {
	i.stopOnce.Do(func() {
		if i.server != nil {
			if err := i.server.Stop(); err != nil {
				i.stopErr = err
			}
		}
		if i.ca != nil {
			if err := i.ca.Cleanup(); err != nil && i.stopErr == nil {
				i.stopErr = err
			}
		}
		close(i.events)
	})
	return i.stopErr
}

// Events returns the channel onto which the Injector publishes
// fault-decision events.
func (i *Injector) Events() <-chan inject.Event {
	return i.events
}

// Proxy returns the underlying martian proxy so callers can install
// request/response modifiers and customize the round tripper.
func (i *Injector) Proxy() *martian.Proxy {
	if i.server == nil {
		return nil
	}
	return i.server.Proxy()
}
