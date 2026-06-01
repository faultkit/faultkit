//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/report"
)

// TestEBPFFlakyNetworkInjectsECONNRESET exercises the eBPF injector end to
// end: faultkit loads the flaky-network BPF program, attaches the recvmsg/
// recvfrom kprobes, and bpf_override_return rewrites the target's recv()
// to ECONNRESET. The target is a libc-based Python client — Go would use
// read() and bypass the kprobes. A local TCP server (outside faultkit's
// PID tree, so its own recv isn't faulted) gives the client something to
// connect to, so the failure lands on recv rather than connect.
//
// Requires Linux/amd64 + root (or CAP_BPF/CAP_NET_ADMIN/CAP_PERFMON) and a
// kernel with BPF kprobe override. Skips when those aren't available,
// including when the injector can't load (surfaced as faultkit's
// internal-error exit code).
func TestEBPFFlakyNetworkInjectsECONNRESET(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("eBPF flaky-network requires linux/amd64; have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root or CAP_BPF+CAP_NET_ADMIN+CAP_PERFMON; run with sudo")
	}
	if !havePython3WithModule("socket") {
		t.Skip("python3 not available")
	}

	root := repoRoot(t)
	faultkit := filepath.Join(root, "bin", "faultkit")
	if _, err := os.Stat(faultkit); err != nil {
		t.Fatalf("bin/faultkit not built; run `make build` first")
	}

	// Local TCP server, NOT wrapped by faultkit, so its own recv syscalls
	// aren't in the faulted PID tree. It accepts, drains the request, sends
	// a tiny reply, and closes; the client's recv is what gets the injected
	// ECONNRESET.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Read(make([]byte, 512))
				_, _ = c.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nok"))
			}(conn)
		}
	}()

	scenarioYAML := filepath.Join(root, "test", "integration", "testdata", "ebpf_flaky", "scenario.yaml")
	target := filepath.Join(root, "test", "integration", "testdata", "ebpf_flaky", "recv_target.py")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command(faultkit, "run",
		"--config", scenarioYAML,
		"--report", reportPath,
		"--verbose",
		"--",
		"python3", target, ln.Addr().String(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	t.Logf("target stdout:\n%s", stdout.String())
	t.Logf("faultkit stderr:\n%s", stderr.String())

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected the target to fail under fault (ExitError), got %v", err)
	}
	switch exitErr.ExitCode() {
	case 2: // ExitInternalError: injector couldn't load on this host.
		t.Skipf("eBPF injector failed to start (kernel may lack BPF kprobe override / BTF):\n%s", stderr.String())
	case 1: // ExitTargetFailed: the fault fired and broke the target. Expected.
	default:
		t.Fatalf("exit code = %d, want 1 (target failed under fault); stderr=%s", exitErr.ExitCode(), stderr.String())
	}

	// The injected syscall return reaches the client as a reset connection.
	if !strings.Contains(stderr.String(), "ConnectionResetError") {
		t.Errorf("target stderr should contain ConnectionResetError from the injected ECONNRESET:\n%s", stderr.String())
	}

	// The report must record at least one fired syscall fault.
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
	sawSyscall := false
	for _, e := range summary.Events {
		if e.Fired && (e.Syscall == "recvmsg" || e.Syscall == "recvfrom") {
			sawSyscall = true
		}
	}
	if !sawSyscall {
		t.Errorf("expected a fired event for recvmsg/recvfrom; events=%+v", summary.Events)
	}
}
