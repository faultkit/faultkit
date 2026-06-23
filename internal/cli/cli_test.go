package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/cli"
	"github.com/faultkit/faultkit/internal/inject"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.ExecuteWith(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runCLI(t, "version")
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d", code, cli.ExitOK)
	}
	if !strings.Contains(out, "faultkit") {
		t.Errorf("output missing 'faultkit': %q", out)
	}
}

func TestVersionShort(t *testing.T) {
	code, out, _ := runCLI(t, "version", "--short")
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d", code, cli.ExitOK)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("--short should produce one line, got: %q", out)
	}
}

func TestScenarioList(t *testing.T) {
	code, out, _ := runCLI(t, "scenario", "list")
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d", code, cli.ExitOK)
	}
	for _, want := range []string{"llm-api-degraded", "flaky-network"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestScenarioShow(t *testing.T) {
	code, out, _ := runCLI(t, "scenario", "show", "llm-api-degraded")
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d", code, cli.ExitOK)
	}
	if !strings.Contains(out, "name: llm-api-degraded") {
		t.Errorf("output missing scenario yaml: %q", out)
	}
}

func TestScenarioShowUnknown(t *testing.T) {
	code, _, stderr := runCLI(t, "scenario", "show", "does-not-exist")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "does-not-exist") {
		t.Errorf("stderr missing scenario name: %q", stderr)
	}
}

func TestUnknownCommandExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "no-such-command")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestScenarioShowExtraArgsExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "scenario", "show", "llm-api-degraded", "extra")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestRunRequiresScenarioOrConfig(t *testing.T) {
	code, _, stderr := runCLI(t, "run", "--", "true")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "scenario") && !strings.Contains(stderr, "config") {
		t.Errorf("stderr should mention --scenario or --config: %q", stderr)
	}
}

func TestRunMutuallyExclusiveSources(t *testing.T) {
	code, _, _ := runCLI(t, "run",
		"--scenario", "llm-api-degraded",
		"--config", "/tmp/whatever.yaml",
		"--", "true",
	)
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestRunUnknownScenarioExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "run", "--scenario", "does-not-exist", "--", "true")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestRunMissingTargetExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "run", "--scenario", "llm-api-degraded")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestRunTargetSucceedsNoFaultFires(t *testing.T) {
	code, _, _ := runCLI(t, "run", "--scenario", "llm-api-degraded", "--", "true")
	if code != cli.ExitFaultNotFired {
		t.Fatalf("code=%d, want %d", code, cli.ExitFaultNotFired)
	}
}

func TestRunTargetFailsExitsTargetFailed(t *testing.T) {
	code, _, _ := runCLI(t, "run", "--scenario", "llm-api-degraded", "--", "false")
	if code != cli.ExitTargetFailed {
		t.Fatalf("code=%d, want %d", code, cli.ExitTargetFailed)
	}
}

func TestRunModeEBPFOnHTTPScenarioRejected(t *testing.T) {
	code, _, stderr := runCLI(t, "run",
		"--scenario", "llm-api-degraded",
		"--mode", "ebpf",
		"--", "true",
	)
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitUsage, stderr)
	}
}

func TestCheckPrintsPlatformAndModes(t *testing.T) {
	code, out, _ := runCLI(t, "check")
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d", code, cli.ExitOK)
	}
	for _, want := range []string{"platform:", "proxy", "ebpf", "mode:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestRunVerboseLogsInjectorActivity(t *testing.T) {
	code, _, stderr := runCLI(t, "run", "--scenario", "llm-api-degraded", "--verbose", "--", "true")
	if code != cli.ExitFaultNotFired {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitFaultNotFired, stderr)
	}
	for _, want := range []string{"[faultkit]", "mode=proxy", "scenario=llm-api-degraded"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("verbose stderr missing %q: %s", want, stderr)
		}
	}
}

func TestRunWithoutVerboseIsQuiet(t *testing.T) {
	_, _, stderr := runCLI(t, "run", "--scenario", "llm-api-degraded", "--", "true")
	if strings.Contains(stderr, "[faultkit]") {
		t.Errorf("non-verbose run should not emit [faultkit] lines: %s", stderr)
	}
}

func TestRunEBPFUnavailableHostGivesClearError(t *testing.T) {
	ebpfAvailable := false
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeEBPF {
			ebpfAvailable = r.Available
		}
	}
	if ebpfAvailable {
		t.Skip("eBPF is available on this host; the unavailable-path guard is a no-op here")
	}
	// On a host where `check` reports eBPF unavailable, `run` must fail fast
	// with that same reason rather than an opaque injector load/attach error.
	code, _, stderr := runCLI(t, "run", "--mode", "ebpf", "--scenario", "flaky-network", "--", "true")
	if code != cli.ExitInternalError {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitInternalError, stderr)
	}
	if !strings.Contains(stderr, "not available") {
		t.Errorf("stderr should explain eBPF is unavailable on this host: %q", stderr)
	}
}

func TestRunModeProxyOnSyscallScenarioRejected(t *testing.T) {
	code, _, stderr := runCLI(t, "run",
		"--scenario", "flaky-network",
		"--mode", "proxy",
		"--", "true",
	)
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitUsage, stderr)
	}
}

func TestValidate_AcceptsBuiltInShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.yaml")
	body := []byte(`
name: my-scenario
experiments:
  - name: e
    fault: {http_status: 500}
    match: {host: a}
    probability: 0.1
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, stderr := runCLI(t, "validate", path)
	if code != cli.ExitOK {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitOK, stderr)
	}
}

func TestValidate_RejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: : valid:::\n  - syntax"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, stderr := runCLI(t, "validate", path)
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d (stderr=%q)", code, cli.ExitUsage, stderr)
	}
	if !strings.Contains(stderr, "kebab-case") && !strings.Contains(stderr, "no experiments") && !strings.Contains(stderr, "parsing scenario yaml") {
		t.Errorf("stderr should explain the validation failure, got %q", stderr)
	}
}

func TestValidate_MissingFileExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "validate", "/nonexistent/path/missing.yaml")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}

func TestValidate_NoArgsExitsUsage(t *testing.T) {
	code, _, _ := runCLI(t, "validate")
	if code != cli.ExitUsage {
		t.Fatalf("code=%d, want %d", code, cli.ExitUsage)
	}
}
