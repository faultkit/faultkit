package proxy_test

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy"
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
