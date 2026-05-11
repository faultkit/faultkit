package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Runner forks target processes, streams their stdio, and reports
// their exit status. Context cancellation propagates to the target as
// SIGINT (with SIGKILL fallback after waitDelay).
type Runner struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// OnStarted, if non-nil, is called once after the target has been
	// forked and its PID is known, before Wait — for consumers that
	// need the post-fork PID.
	OnStarted func(pid int)
}

const waitDelay = 5 * time.Second

// Result is the outcome of running a target to completion.
type Result struct {
	ExitCode int
	Duration time.Duration
}

// Run forks target with env merged on top of the parent environment,
// streams stdout/stderr through the Runner's writers, and waits for
// the target to exit. The returned error is non-nil only on internal
// failures (start failed, wait surfaced a non-exit error). A target
// that exits non-zero is reported via Result.ExitCode with err == nil.
func (r *Runner) Run(ctx context.Context, target []string, env []string) (*Result, error) {
	if len(target) == 0 {
		return nil, errors.New("runner: empty target command")
	}

	stdout := r.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := r.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := r.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	cmd := exec.CommandContext(ctx, target[0], target[1:]...) // #nosec G204 -- target is the user-provided command after `--`; running it is the contract.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = stdin
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Signal(syscall.SIGINT)
		}
		return nil
	}
	cmd.WaitDelay = waitDelay

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: start %s: %w", target[0], err)
	}
	if r.OnStarted != nil && cmd.Process != nil {
		r.OnStarted(cmd.Process.Pid)
	}

	err := cmd.Wait()
	duration := time.Since(start)

	if err == nil {
		return &Result{ExitCode: 0, Duration: duration}, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &Result{ExitCode: exitErr.ExitCode(), Duration: duration}, nil
	}
	return nil, fmt.Errorf("runner: wait %s: %w", target[0], err)
}
