package proxy

import (
	"fmt"
	"net"

	"github.com/google/martian/v3"
	"github.com/google/martian/v3/mitm"
)

// Server runs the martian-based MITM proxy. It binds a listener on
// 127.0.0.1 with an OS-assigned port and forwards traffic via martian.
type Server struct {
	proxy *martian.Proxy
	ln    net.Listener
}

// NewServer constructs a Server using ca as the MITM signing root.
func NewServer(ca *CA) (*Server, error) {
	config, err := mitm.NewConfig(ca.Cert(), ca.Key())
	if err != nil {
		return nil, fmt.Errorf("server: mitm config: %w", err)
	}
	config.SetValidity(caValidity)
	config.SetOrganization("faultkit")

	p := martian.NewProxy()
	p.SetMITM(config)
	return &Server{proxy: p}, nil
}

// Listen binds the proxy to 127.0.0.1:0 and serves in a background
// goroutine. Returns the bound address (host:port).
func (s *Server) Listen() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("server: listen: %w", err)
	}
	s.ln = ln
	go func() { _ = s.proxy.Serve(ln) }()
	return ln.Addr().String(), nil
}

// Stop tears down the proxy and its listener. Idempotent.
func (s *Server) Stop() error {
	if s.proxy != nil {
		s.proxy.Close()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	return nil
}

// Proxy returns the underlying martian proxy so callers can install
// request/response modifiers and customize the round tripper.
func (s *Server) Proxy() *martian.Proxy { return s.proxy }
