package proxy_test

import (
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/faultkit-dev/faultkit/internal/inject/proxy"
)

func TestNewCAIsRoot(t *testing.T) {
	ca, err := proxy.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	cert := ca.Cert()
	if !cert.IsCA {
		t.Error("CA cert should have IsCA=true")
	}
	if !cert.BasicConstraintsValid {
		t.Error("CA cert should have BasicConstraintsValid")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA cert should have KeyUsageCertSign")
	}
}

func TestMintLeafSignedByRoot(t *testing.T) {
	ca, err := proxy.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	leaf, err := ca.MintLeaf("api.openai.com")
	if err != nil {
		t.Fatalf("MintLeaf: %v", err)
	}
	if leaf.Leaf == nil {
		t.Fatal("leaf.Leaf is nil")
	}
	if got := leaf.Leaf.DNSNames; len(got) != 1 || got[0] != "api.openai.com" {
		t.Errorf("DNSNames = %v, want [api.openai.com]", got)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert())
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		t.Errorf("leaf does not verify against root: %v", err)
	}
}

func TestMintLeafIPSAN(t *testing.T) {
	ca, err := proxy.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	leaf, err := ca.MintLeaf("127.0.0.1")
	if err != nil {
		t.Fatalf("MintLeaf: %v", err)
	}
	want := net.IPv4(127, 0, 0, 1)
	if len(leaf.Leaf.IPAddresses) != 1 || !leaf.Leaf.IPAddresses[0].Equal(want) {
		t.Errorf("IPAddresses = %v, want [%v]", leaf.Leaf.IPAddresses, want)
	}
	if len(leaf.Leaf.DNSNames) != 0 {
		t.Errorf("DNSNames should be empty for IP host, got %v", leaf.Leaf.DNSNames)
	}
}

func TestMintLeafCachesPerHost(t *testing.T) {
	ca, err := proxy.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	a, _ := ca.MintLeaf("a.example.com")
	b, _ := ca.MintLeaf("a.example.com")
	if a != b {
		t.Error("MintLeaf should return the cached cert for the same host")
	}
	c, _ := ca.MintLeaf("b.example.com")
	if c == a {
		t.Error("different hosts should get different leaves")
	}
}

func TestWriteCertPEMAndCleanup(t *testing.T) {
	ca, err := proxy.NewCA()
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}

	path, err := ca.WriteCertPEM()
	if err != nil {
		t.Fatalf("WriteCertPEM: %v", err)
	}
	if filepath.Base(path) == "" {
		t.Errorf("WriteCertPEM returned empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("PEM file should exist: %v", err)
	}

	if err := ca.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PEM file should be removed after Cleanup, stat err = %v", err)
	}

	// Idempotent.
	if err := ca.Cleanup(); err != nil {
		t.Errorf("second Cleanup should be a no-op, got %v", err)
	}
}
