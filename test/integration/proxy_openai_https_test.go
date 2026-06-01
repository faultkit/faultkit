//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/report"
)

// TestProxyOpenAIHTTPSDemoPath exercises the marquee demo path end to
// end: the real openai-python SDK, over HTTPS to api.openai.com, with
// zero client-side TLS configuration. faultkit's injected HTTPS_PROXY
// routes the request through the MITM proxy and SSL_CERT_FILE makes the
// SDK trust the per-run CA. The synthetic 429 fires before any upstream
// round trip, so the test runs fully offline.
//
// This is deliberately distinct from TestProxyAgainstOpenAIPython, which
// uses an http:// loopback mock and a manually-mounted proxy transport —
// that test never exercises TLS termination or the env-var CA trust
// injection that the production demo relies on.
func TestProxyOpenAIHTTPSDemoPath(t *testing.T) {
	if !havePython3WithModule("openai") {
		t.Skip("python3 with openai SDK not available; install: pip install openai")
	}
	if !havePython3WithModule("pytest") {
		t.Skip("python3 with pytest not available; install: pip install pytest")
	}

	root := repoRoot(t)
	faultkit := filepath.Join(root, "bin", "faultkit")
	if _, err := os.Stat(faultkit); err != nil {
		t.Fatalf("bin/faultkit not built; run `make build` first")
	}

	dir := filepath.Join(root, "test", "integration", "testdata", "openai_https")
	scenarioYAML := filepath.Join(dir, "scenario.yaml")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkit, "run",
		"--config", scenarioYAML,
		"--report", reportPath,
		"--",
		"python3", "-m", "pytest", "-v", dir,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	t.Logf("pytest stdout:\n%s", stdout.String())
	t.Logf("faultkit stderr:\n%s", stderr.String())

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1 (target failed under fault), got %d", exitErr.ExitCode())
	}

	out := stdout.String()
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected pytest output to contain FAILED")
	}
	// The SDK only raises RateLimitError on a real 429 response, so this
	// proves the synthetic fault reached the client through the MITM'd
	// TLS connection — not just that faultkit ran.
	if !strings.Contains(out, "RateLimitError") {
		t.Errorf("expected the SDK to raise RateLimitError from the injected 429")
	}

	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var summary report.Summary
	if err := json.Unmarshal(reportData, &summary); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if summary.TargetExit != 1 {
		t.Errorf("report.target_exit = %d, want 1", summary.TargetExit)
	}
	if summary.FiredCount() == 0 {
		t.Fatalf("expected at least one fired event in report; got 0")
	}
	// The defining assertion for the TLS path: the fault fired against
	// the real api.openai.com host. That only happens if the proxy MITM'd
	// the HTTPS CONNECT, minted a leaf for that host, and the SDK trusted
	// the per-run CA via the injected SSL_CERT_FILE.
	sawHost := false
	for _, e := range summary.Events {
		if e.Fired && e.Host == "api.openai.com" {
			sawHost = true
		}
	}
	if !sawHost {
		t.Errorf("expected a fired event with host=api.openai.com; events=%+v", summary.Events)
	}
}
