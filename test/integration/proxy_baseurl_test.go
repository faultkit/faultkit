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

// TestBaseURLAgainstOpenAINode exercises base-URL/origin mode end-to-end
// against the real openai-node SDK. Like much of the Node ecosystem, the
// SDK's HTTP layer (fetch/undici) ignores HTTPS_PROXY, so forward-proxy
// mode would not intercept it — but base-URL mode does, because the SDK
// reads OPENAI_BASE_URL, which `faultkit run --base-url` injects.
//
// Supply chain: the example's only dependency (openai) is pinned in
// package-lock.json and installed with `npm ci`.
func TestBaseURLAgainstOpenAINode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	root := repoRoot(t)
	nodeDir := filepath.Join(root, "examples", "llm-api-degraded", "nodejs")
	if _, err := os.Stat(filepath.Join(nodeDir, "node_modules", "openai")); err != nil {
		t.Skip("openai SDK not installed; run `npm ci` in examples/llm-api-degraded/nodejs")
	}
	faultkit := filepath.Join(root, "bin", "faultkit")
	if _, err := os.Stat(faultkit); err != nil {
		t.Fatalf("bin/faultkit not built; run `make build` first")
	}

	// Deterministic: always inject a 429 on OpenAI so the run is repeatable.
	scenarioPath := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenarioPath, []byte(`name: baseurl-degraded
experiments:
  - name: always-429
    fault:
      http_status: 429
    match:
      host: api.openai.com
      path: /v1/*
    probability: 1.0
`), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkit, "run",
		"--base-url",
		"--config", scenarioPath,
		"--report", reportPath,
		"--", "node", filepath.Join(nodeDir, "baseurl-client.js"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	t.Logf("node stdout:\n%s", stdout.String())
	t.Logf("faultkit stderr:\n%s", stderr.String())

	// node exits 7 on GOT_429; faultkit maps a non-zero target exit to
	// ExitTargetFailed (1).
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError (target failed under fault), got %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("faultkit exit = %d, want 1 (target failed under fault)", exitErr.ExitCode())
	}

	// The SDK ignores HTTPS_PROXY; receiving the 429 proves base-URL mode
	// reached it through the injected OPENAI_BASE_URL.
	if !strings.Contains(stdout.String(), "GOT_429") {
		t.Errorf("node client did not receive the injected 429 via base-URL")
	}

	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var summary report.Summary
	if err := json.Unmarshal(reportData, &summary); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if summary.FiredCount() == 0 {
		t.Errorf("report shows no fired fault; base-URL interception did not fire")
	}
}
