package runner_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/faultkit/faultkit/internal/runner"
)

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("posix-only test")
	}
}

func TestRunSuccess(t *testing.T) {
	skipIfWindows(t)
	r := &runner.Runner{Stdout: io.Discard, Stderr: io.Discard}
	res, err := r.Run(context.Background(), []string{"sh", "-c", "exit 0"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration should be positive, got %v", res.Duration)
	}
}

func TestRunNonzeroExitInResult(t *testing.T) {
	skipIfWindows(t)
	r := &runner.Runner{Stdout: io.Discard, Stderr: io.Discard}
	res, err := r.Run(context.Background(), []string{"sh", "-c", "exit 7"}, nil)
	if err != nil {
		t.Fatalf("non-zero exit must surface via Result, not err; got %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
}

func TestRunPropagatesEnv(t *testing.T) {
	skipIfWindows(t)
	var stdout bytes.Buffer
	r := &runner.Runner{Stdout: &stdout, Stderr: io.Discard}
	_, err := r.Run(context.Background(),
		[]string{"sh", "-c", "echo $HTTPS_PROXY"},
		[]string{"HTTPS_PROXY=http://127.0.0.1:9999"},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "http://127.0.0.1:9999" {
		t.Errorf("stdout = %q, want http://127.0.0.1:9999", got)
	}
}

func TestRunInjectedEnvOverridesParent(t *testing.T) {
	skipIfWindows(t)
	t.Setenv("FAULTKIT_TEST_OVERRIDE", "from-parent")

	var stdout bytes.Buffer
	r := &runner.Runner{Stdout: &stdout, Stderr: io.Discard}
	_, err := r.Run(context.Background(),
		[]string{"sh", "-c", "echo $FAULTKIT_TEST_OVERRIDE"},
		[]string{"FAULTKIT_TEST_OVERRIDE=from-injector"},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "from-injector" {
		t.Errorf("stdout = %q, want from-injector", got)
	}
}

func TestRunEmptyTargetErrors(t *testing.T) {
	r := &runner.Runner{Stdout: io.Discard, Stderr: io.Discard}
	_, err := r.Run(context.Background(), nil, nil)
	if err == nil {
		t.Error("got nil, want error for empty target")
	}
}

func TestRunBadCommandErrors(t *testing.T) {
	r := &runner.Runner{Stdout: io.Discard, Stderr: io.Discard}
	res, err := r.Run(context.Background(), []string{"/no/such/binary"}, nil)
	if err == nil {
		t.Errorf("got nil err with res=%+v, want internal error for missing binary", res)
	}
}

func TestRunCtxCancelKillsTarget(t *testing.T) {
	skipIfWindows(t)

	// `sleep 30` runs until SIGINT. Canceling ctx triggers cmd.Cancel
	// which sends SIGINT; if anything regresses, the duration check
	// catches it well before the natural 30s wait.
	target := []string{"sleep", "30"}
	r := &runner.Runner{Stdout: io.Discard, Stderr: io.Discard}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res, err := r.Run(ctx, target, nil)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("Run took %v; ctx cancel should have killed the target almost immediately", elapsed)
	}
	if errors.Is(err, context.Canceled) {
		t.Logf("Run surfaced ctx.Canceled, ok")
		return
	}
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil || res.ExitCode == 0 {
		t.Errorf("expected non-zero exit on ctx cancel; got res=%+v", res)
	}
}
