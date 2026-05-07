package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/martian/v3/mitm"
)

// caValidity bounds the per-run CA's lifetime. A faultkit run is
// expected to clean up well before this; the cap exists so any leaked
// cert expires on its own.
const caValidity = 24 * time.Hour

// CA is a per-run ephemeral certificate authority. Cert and key live
// only in process memory plus, optionally, a temp PEM file written for
// clients that need an `SSL_CERT_FILE`-style path. Cleanup removes the
// file; nothing is ever installed in a system trust store.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu      sync.Mutex
	leaves  map[string]*tls.Certificate
	pemPath string
}

// NewCA generates a new ephemeral root.
func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ca: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, mitm.MaxSerialNumber)
	if err != nil {
		return nil, fmt.Errorf("ca: serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "faultkit ephemeral root",
			Organization: []string{"faultkit"},
		},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("ca: create cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca: parse cert: %w", err)
	}

	return &CA{
		cert:   cert,
		key:    key,
		leaves: make(map[string]*tls.Certificate),
	}, nil
}

// Cert returns the CA's *x509.Certificate.
func (c *CA) Cert() *x509.Certificate { return c.cert }

// Key returns the CA's signing key.
func (c *CA) Key() *ecdsa.PrivateKey { return c.key }

// MintLeaf returns a leaf certificate for host, signed by the CA.
// Hosts are normalized (lowercased, trailing dot stripped, optional
// port stripped) before lookup, so equivalent inputs share a cache
// entry.
func (c *CA) MintLeaf(host string) (*tls.Certificate, error) {
	host = normalizeHost(host)

	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.leaves[host]; ok {
		return cached, nil
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("leaf: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, mitm.MaxSerialNumber)
	if err != nil {
		return nil, fmt.Errorf("leaf: serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(caValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &leafKey.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("leaf: create cert: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("leaf: parse cert: %w", err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{der, c.cert.Raw},
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}
	c.leaves[host] = tlsCert
	return tlsCert, nil
}

// WriteCertPEM writes the CA cert in PEM form to a temp file and
// returns its path. The path is tracked for Cleanup.
func (c *CA) WriteCertPEM() (string, error) {
	f, err := os.CreateTemp("", "faultkit-ca-*.pem")
	if err != nil {
		return "", fmt.Errorf("ca: tempfile: %w", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw}); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("ca: write pem: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("ca: close pem: %w", err)
	}

	c.mu.Lock()
	prev := c.pemPath
	c.pemPath = f.Name()
	c.mu.Unlock()
	if prev != "" {
		_ = os.Remove(prev)
	}
	return f.Name(), nil
}

// Cleanup removes the PEM file, if any. Idempotent.
func (c *CA) Cleanup() error {
	c.mu.Lock()
	path := c.pemPath
	c.pemPath = ""
	c.mu.Unlock()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ca: cleanup: %w", err)
	}
	return nil
}

func normalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host
}
