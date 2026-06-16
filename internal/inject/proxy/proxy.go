package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/martian/v3"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/pkg/scenario"
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

// Injector implements inject.Injector for the HTTPS proxy mechanism. It
// has two interception paths: the default forward-proxy (HTTPS_PROXY + CA
// MITM) and base-URL/origin mode (the target's SDK is pointed at faultkit
// via *_BASE_URL env vars). See UseBaseURL.
type Injector struct {
	ca     *CA
	server *Server // forward-proxy mode

	originSrv *http.Server // base-URL mode
	originLn  net.Listener
	baseURL   bool

	events chan inject.Event
	vlog   inject.Logf

	stopOnce sync.Once
	stopErr  error
}

// SetVerbose installs a verbose logger. Implements inject.VerboseAware.
func (i *Injector) SetVerbose(vlog inject.Logf) { i.vlog = vlog }

// UseBaseURL switches the injector into base-URL ("origin") mode: instead
// of relying on the target honoring HTTPS_PROXY, faultkit points the
// target's SDK at itself via provider base-URL env vars (OPENAI_BASE_URL,
// ANTHROPIC_BASE_URL, …). Use this for clients that ignore proxy env (much
// of the Node/SDK ecosystem). Must be called before Start.
func (i *Injector) UseBaseURL(enabled bool) { i.baseURL = enabled }

// New returns a new, unstarted Injector.
func New() *Injector {
	return &Injector{events: make(chan inject.Event, eventBuffer)}
}

// Start generates a per-run CA, starts the proxy on 127.0.0.1:0,
// installs the fault matcher and faulter on it, and returns the env
// vars the runner must merge into the target's environment so its
// HTTP/HTTPS traffic flows through the proxy and trusts our CA.
func (i *Injector) Start(_ context.Context, s *scenario.Scenario) ([]string, error) {
	if i.server != nil || i.originSrv != nil {
		return nil, errors.New("proxy: already started")
	}
	if i.baseURL {
		return i.startBaseURL(s)
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

	faulter := NewFaulter(s, i.events, nil)
	server.Proxy().SetRequestModifier(faulter)
	server.Proxy().SetResponseModifier(faulter)

	addr, err := server.Listen()
	if err != nil {
		_ = ca.Cleanup()
		return nil, err
	}
	i.server = server

	if i.vlog != nil {
		i.vlog("proxy: listening on %s; target trusts per-run CA at %s", addr, pemPath)
	}

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

// startBaseURL starts the origin-mode HTTP server and returns the
// *_BASE_URL env the runner injects so the target's SDK connects to
// faultkit directly. No CA/MITM is needed: the client talks plain HTTP to
// faultkit, which forwards over real TLS to the upstream.
func (i *Injector) startBaseURL(s *scenario.Scenario) ([]string, error) {
	providers := providersForHostGlobs(scenarioHTTPHosts(s))
	if len(providers) == 0 {
		return nil, fmt.Errorf("proxy: base-URL mode supports none of this scenario's hosts (known providers: %s)", providerIDList(providerRegistry))
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy: base-url listen: %w", err)
	}
	addr := ln.Addr().String()

	faulter := NewFaulter(s, i.events, nil)
	srv := &http.Server{
		Handler:           newOriginHandler(faulter, i.vlog),
		ReadHeaderTimeout: 30 * time.Second,
	}
	i.originSrv = srv
	i.originLn = ln
	go func() { _ = srv.Serve(ln) }()

	if i.vlog != nil {
		i.vlog("proxy: base-URL mode on %s; providers: %s", addr, providerIDList(providers))
	}

	env := make([]string, 0, len(providers)*2+2)
	for _, p := range providers {
		for _, k := range p.baseURLEnv {
			env = append(env, k+"="+p.baseURL(addr))
		}
	}
	// Keep an ambient proxy from intercepting the loopback hop to faultkit.
	env = append(env, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
	return env, nil
}

// scenarioHTTPHosts returns the non-empty match hosts of s's HTTP
// experiments, used to derive which providers base-URL mode should target.
func scenarioHTTPHosts(s *scenario.Scenario) []string {
	if s == nil {
		return nil
	}
	var hosts []string
	for _, exp := range s.Experiments {
		if exp.Match.IsHTTP() && exp.Match.Host != "" {
			hosts = append(hosts, exp.Match.Host)
		}
	}
	return hosts
}

// Stop tears down the proxy and removes the CA temp file. Idempotent.
func (i *Injector) Stop(_ context.Context) error {
	i.stopOnce.Do(func() {
		if i.server != nil {
			if err := i.server.Stop(); err != nil {
				i.stopErr = err
			}
		}
		if i.originSrv != nil {
			if err := i.originSrv.Close(); err != nil && i.stopErr == nil {
				i.stopErr = err
			}
		}
		if i.originLn != nil {
			_ = i.originLn.Close()
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
