//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These drive the Anthropic-distinctive failure modes end-to-end through
// base-URL/origin mode against a real out-of-process HTTP client. All are
// synthesized offline, so they run without a network or API key.

func runAnthropic(t *testing.T, failure string) (stdout string, reportPath string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	client := filepath.Join(repoRoot(t), "test", "integration", "testdata", "baseurl_client", "anthropic.mjs")
	scenario := writeScenario(t, "name: anthropic-verify\nexperiments:\n  - name: x\n    failure: "+failure+"\n    probability: 1.0\n")
	reportPath = filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkitBin(t), "run",
		"--base-url",
		"--config", scenario,
		"--report", reportPath,
		"--", "node", client,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("faultkit run failed: %v\nstderr:\n%s", err, errBuf.String())
	}
	t.Logf("client stdout:\n%s", out.String())
	return out.String(), reportPath
}

func bodyLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "BODY="); ok {
			return rest
		}
	}
	return ""
}

func TestAnthropicStreamError(t *testing.T) {
	out, reportPath := runAnthropic(t, "stream-error")
	if !strings.Contains(out, "STATUS=200") {
		t.Errorf("want STATUS=200:\n%s", out)
	}
	body := bodyLine(out)
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "overloaded_error") {
		t.Errorf("stream should contain a mid-stream error event:\n%s", body)
	}
	if strings.Contains(body, "message_stop") {
		t.Errorf("cut stream must NOT contain message_stop:\n%s", body)
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("no fired fault in report")
	}
}

func TestAnthropicToolUseCutoff(t *testing.T) {
	out, reportPath := runAnthropic(t, "tool-use-cutoff")
	if !strings.Contains(out, "STATUS=200") {
		t.Errorf("want STATUS=200:\n%s", out)
	}
	var env struct {
		Content    []struct{ Type string } `json:"content"`
		StopReason string                  `json:"stop_reason"`
	}
	if err := json.Unmarshal([]byte(bodyLine(out)), &env); err != nil {
		t.Fatalf("envelope must be valid JSON: %v", err)
	}
	if env.StopReason != "max_tokens" {
		t.Errorf("stop_reason = %q, want max_tokens (truncated tool call)", env.StopReason)
	}
	hasTool := false
	for _, c := range env.Content {
		if c.Type == "tool_use" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Errorf("expected a tool_use block in the truncated response")
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("no fired fault in report")
	}
}

func TestAnthropicRefusal(t *testing.T) {
	out, reportPath := runAnthropic(t, "refusal")
	if !strings.Contains(out, "STATUS=200") {
		t.Errorf("want STATUS=200:\n%s", out)
	}
	if !strings.Contains(bodyLine(out), `"stop_reason":"refusal"`) {
		t.Errorf("want stop_reason refusal:\n%s", bodyLine(out))
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("no fired fault in report")
	}
}

func TestAnthropicRequestTooLarge(t *testing.T) {
	out, reportPath := runAnthropic(t, "request-too-large")
	if !strings.Contains(out, "STATUS=413") {
		t.Errorf("want STATUS=413:\n%s", out)
	}
	if !strings.Contains(bodyLine(out), "request_too_large") {
		t.Errorf("want Anthropic request_too_large shape:\n%s", bodyLine(out))
	}
	if readReport(t, reportPath).FiredCount() == 0 {
		t.Errorf("no fired fault in report")
	}
}
