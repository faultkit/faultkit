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

// These tests drive the failure-mode × provider model end-to-end through
// base-URL/origin mode against real out-of-process clients. They prove the
// whole new pipeline runs for real: a fixture-driven scenario → expansion →
// per-provider fixture → origin server → a real HTTP client receiving the
// provider-shaped fault. Faults are synthesized offline, so no real API key
// or network to the provider is required.

func faultkitBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(repoRoot(t), "bin", "faultkit")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("bin/faultkit not built; run `make build` first")
	}
	return bin
}

// writeScenario writes a deterministic (probability 1.0) fixture-driven
// scenario and returns its path.
func writeScenario(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	return p
}

func readReport(t *testing.T, path string) report.Summary {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var s report.Summary
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	return s
}

// TestFixtureModeOpenAIRateLimited drives the migrated `rate-limited` failure
// mode against the REAL openai-node SDK via base-URL mode. The SDK ignores
// HTTPS_PROXY, so receiving the 429 proves the fixture-driven scenario expanded
// to the OpenAI provider and intercepted a real vendor SDK call.
func TestFixtureModeOpenAIRateLimited(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	root := repoRoot(t)
	nodeDir := filepath.Join(root, "examples", "llm-api-degraded", "nodejs")
	if _, err := os.Stat(filepath.Join(nodeDir, "node_modules", "openai")); err != nil {
		t.Skip("openai SDK not installed; run `npm ci` in examples/llm-api-degraded/nodejs")
	}

	scenario := writeScenario(t, `name: fixturemode-ratelimited
experiments:
  - name: rl
    failure: rate-limited
    provider: openai
    probability: 1.0
`)
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkitBin(t), "run",
		"--base-url",
		"--config", scenario,
		"--report", reportPath,
		"--", "node", filepath.Join(nodeDir, "baseurl-client.js"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	t.Logf("client stdout:\n%s\nfaultkit stderr:\n%s", stdout.String(), stderr.String())

	// The client exits 7 on GOT_429 → faultkit maps a non-zero target exit to 1.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("want faultkit exit 1 (target failed under fault), got err=%v", err)
	}
	if !strings.Contains(stdout.String(), "GOT_429") {
		t.Errorf("openai SDK did not receive the injected 429 via the fixture-driven scenario")
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("report shows no fired fault")
	}
}

// TestFixtureModeAnthropicOverloaded drives the Anthropic-only `overloaded`
// mode (HTTP 529) and asserts the client receives the Anthropic-shaped
// overloaded_error body.
func TestFixtureModeAnthropicOverloaded(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	root := repoRoot(t)
	client := filepath.Join(root, "test", "integration", "testdata", "baseurl_client", "anthropic.mjs")

	scenario := writeScenario(t, `name: fixturemode-overloaded
experiments:
  - name: ov
    failure: overloaded
    probability: 1.0
`)
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkitBin(t), "run",
		"--base-url",
		"--config", scenario,
		"--report", reportPath,
		"--", "node", client,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := stdout.String()
	t.Logf("client stdout:\n%s\nfaultkit stderr:\n%s", out, stderr.String())

	// Client exits 0; fault fired with target ok → faultkit exits 0.
	if err != nil {
		t.Fatalf("faultkit run failed: %v", err)
	}
	if !strings.Contains(out, "STATUS=529") {
		t.Errorf("client did not receive HTTP 529:\n%s", out)
	}
	if !strings.Contains(out, "overloaded_error") {
		t.Errorf("response is not the Anthropic overloaded_error shape:\n%s", out)
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("report shows no fired fault")
	}
}

// TestFixtureModeAnthropicMalformedJSON drives `malformed-json` narrowed to
// Anthropic with --provider, and asserts the envelope is valid Anthropic shape
// while the assistant's text content is itself invalid JSON.
func TestFixtureModeAnthropicMalformedJSON(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	client := filepath.Join(repoRoot(t), "test", "integration", "testdata", "baseurl_client", "anthropic.mjs")

	scenario := writeScenario(t, `name: fixturemode-malformed
experiments:
  - name: mj
    failure: malformed-json
    provider: anthropic
    probability: 1.0
`)
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkitBin(t), "run",
		"--base-url",
		"--config", scenario,
		"--report", reportPath,
		"--", "node", client,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("faultkit run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	out := stdout.String()
	t.Logf("client stdout:\n%s", out)

	if !strings.Contains(out, "STATUS=200") {
		t.Errorf("malformed-json should return HTTP 200:\n%s", out)
	}

	// Extract the BODY= line, confirm the envelope parses but content[0].text
	// (the model's "structured output") does not.
	body := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "BODY="); ok {
			body = rest
		}
	}
	var env struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("Anthropic envelope should be valid JSON: %v\nbody=%s", err, body)
	}
	if len(env.Content) == 0 {
		t.Fatalf("expected content[] in Anthropic envelope:\n%s", body)
	}
	if json.Valid([]byte(env.Content[0].Text)) {
		t.Errorf("content text should be MALFORMED JSON, but it parsed: %q", env.Content[0].Text)
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("report shows no fired fault")
	}
}
