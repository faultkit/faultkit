//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/faultkit/faultkit/internal/report"
)

// TestEBPFToolPermissionDeniedInjectsEACCES exercises the second eBPF
// scenario end to end: faultkit loads the tool-permission-denied program,
// attaches the openat kprobe, and bpf_override_return rewrites the target's
// open() to EACCES. The target is a libc-based Python client.
//
// At probability 1.0 every openat in the target tree fails — including the
// interpreter's own startup opens — so the run fails somewhere in its open
// path. That's the point: openat is denied for the whole target tree. The
// assertions stay on the observable contract (target failed + a fired
// openat fault) rather than where exactly the process died, which is
// timing-dependent.
//
// Requires Linux/amd64 + root (or CAP_BPF/CAP_NET_ADMIN/CAP_PERFMON) and a
// kernel with BPF kprobe override. Skips otherwise, including when the
// injector can't load (faultkit internal-error exit).
func TestEBPFToolPermissionDeniedInjectsEACCES(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("eBPF tool-permission-denied requires linux/amd64; have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root or CAP_BPF+CAP_NET_ADMIN+CAP_PERFMON; run with sudo")
	}
	if !havePython3WithModule("sys") {
		t.Skip("python3 not available")
	}

	root := repoRoot(t)
	faultkit := filepath.Join(root, "bin", "faultkit")
	if _, err := os.Stat(faultkit); err != nil {
		t.Fatalf("bin/faultkit not built; run `make build` first")
	}

	scenarioYAML := filepath.Join(root, "test", "integration", "testdata", "ebpf_tool_perm", "scenario.yaml")
	target := filepath.Join(root, "test", "integration", "testdata", "ebpf_tool_perm", "open_target.py")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkit, "run",
		"--config", scenarioYAML,
		"--report", reportPath,
		"--verbose",
		"--",
		"python3", target, "/etc/hostname",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	t.Logf("target stdout:\n%s", stdout.String())
	t.Logf("faultkit stderr:\n%s", stderr.String())

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected the target to fail under fault (ExitError), got %v", err)
	}
	switch exitErr.ExitCode() {
	case 2: // ExitInternalError: injector couldn't load on this host.
		t.Skipf("eBPF injector failed to start (kernel may lack BPF kprobe override / BTF):\n%s", stderr.String())
	case 1: // ExitTargetFailed: openat denied broke the target. Expected.
	default:
		t.Fatalf("exit code = %d, want 1 (target failed under fault); stderr=%s", exitErr.ExitCode(), stderr.String())
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
		t.Fatalf("expected at least one fired event; report=%s", reportData)
	}
	sawOpenat := false
	for _, e := range summary.Events {
		if e.Fired && e.Syscall == "openat" {
			sawOpenat = true
		}
	}
	if !sawOpenat {
		t.Errorf("expected a fired event for openat; events=%+v", summary.Events)
	}
}
