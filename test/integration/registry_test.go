// SPDX-License-Identifier: Apache-2.0
//go:build integration

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeRegFixture(t *testing.T, dir, relPath, body string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func runFaultkit(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	bin := os.Getenv("FAULTKIT_BIN")
	if bin == "" {
		bin = "faultkit"
	}
	cmd := exec.Command(bin, args...) // #nosec G204 -- test-driven; bin is the configured faultkit binary.
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v", err)
	}
	return code, stdout.String(), stderr.String()
}

func TestIntegration_RegistryRoot_ResolvesAndRuns(t *testing.T) {
	regDir := t.TempDir()
	writeRegFixture(t, regDir, "llm/integration-scenario.yaml", `
name: integration-scenario
description: an integration test scenario
experiments:
  - name: e
    fault: {http_status: 500}
    match: {host: integration.test}
    probability: 0.1
`)

	// 1) The scenario is reachable via --registry-root.
	code, out, stderr := runFaultkit(t, "scenario", "list", "--registry-root", regDir)
	if code != 0 {
		t.Fatalf("scenario list code=%d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(out, "integration-scenario") {
		t.Errorf("list output missing the registry scenario: %q", out)
	}

	// 2) The scenario validates via 'faultkit validate'.
	scenarioPath := filepath.Join(regDir, "llm", "integration-scenario.yaml")
	code, out, stderr = runFaultkit(t, "validate", scenarioPath)
	if code != 0 {
		t.Fatalf("validate code=%d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(out, "ok:") {
		t.Errorf("validate output missing 'ok:': %q", out)
	}

	// 3) The scenario runs end-to-end via --registry-root. We
	//    don't need the fault to actually fire; we just need the
	//    resolver to find the scenario and the runner to start.
	//    ExitFaultNotFired (3) and ExitTargetFailed (1) are both
	//    acceptable here — both prove the path was found.
	code, _, _ = runFaultkit(t, "run",
		"--registry-root", regDir,
		"--scenario", "llm/integration-scenario",
		"--", "true")
	if code != 1 && code != 3 {
		t.Fatalf("run code=%d, want 1 (target failed) or 3 (fault not fired)", code)
	}
}

func TestIntegration_RegistryRoot_PackStyle(t *testing.T) {
	regDir := t.TempDir()
	writeRegFixture(t, regDir, "llm/pack-style/scenario.yaml", `
name: pack-style
description: pack-style scenario
experiments:
  - name: e
    fault: {http_status: 502}
    match: {host: pack.test}
    probability: 0.1
`)

	scenarioPath := filepath.Join(regDir, "llm", "pack-style", "scenario.yaml")
	code, out, stderr := runFaultkit(t, "validate", scenarioPath)
	if code != 0 {
		t.Fatalf("validate code=%d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(out, "ok:") {
		t.Errorf("validate output missing 'ok:': %q", out)
	}
}
