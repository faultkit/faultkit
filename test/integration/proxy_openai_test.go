//go:build integration

// Package integration_test runs end-to-end tests against real client
// SDKs. Skipped by default — invoke with `make test-integration`. Each
// test skips when its prereqs aren't installed rather than failing.
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

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func havePython3WithModule(module string) bool {
	if _, err := exec.LookPath("python3"); err != nil {
		return false
	}
	return exec.Command("python3", "-c", "import "+module).Run() == nil
}

// TestProxyAgainstOpenAIPython exercises the proxy injector end-to-end
// against the real openai-python SDK.
func TestProxyAgainstOpenAIPython(t *testing.T) {
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

	scenarioYAML := filepath.Join(root, "examples", "llm-api-degraded", "scenario.yaml")
	pythonDir := filepath.Join(root, "examples", "llm-api-degraded", "python")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkit, "run",
		"--config", scenarioYAML,
		"--report", reportPath,
		"--",
		"python3", "-m", "pytest", "-v", pythonDir,
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

	if !strings.Contains(stdout.String(), "FAILED") {
		t.Errorf("expected pytest output to contain FAILED")
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
		t.Errorf("expected at least one fired event in report; got 0")
	}
}
