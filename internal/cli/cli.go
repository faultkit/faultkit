// Package cli implements the faultkit command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/internal/runner"
)

// Exit codes returned by Execute. CI scripts branch on these; do not renumber.
const (
	ExitOK            = 0
	ExitTargetFailed  = 1
	ExitInternalError = 2
	ExitFaultNotFired = 3
	ExitUsage         = 4
)

// Execute runs the faultkit CLI with os.Args and returns the exit code.
func Execute() int {
	return ExecuteWith(os.Args[1:], os.Stdout, os.Stderr)
}

// ExecuteWith is the testable form of Execute.
func ExecuteWith(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	fmt.Fprintln(stderr, "Error:", err)

	var targetExit *runner.TargetExitError
	switch {
	case errors.As(err, &targetExit):
		return ExitTargetFailed
	case errors.Is(err, runner.ErrFaultNotFired):
		return ExitFaultNotFired
	case isUsageError(err):
		return ExitUsage
	default:
		return ExitInternalError
	}
}

type usageError struct{ err error }

func (u *usageError) Error() string { return u.err.Error() }
func (u *usageError) Unwrap() error { return u.err }

// UsageErrorf wraps a formatted error so the CLI dispatch maps it to
// ExitUsage. Use for invalid flag combinations and unknown
// scenarios/files surfaced to the user as their own input mistake.
func UsageErrorf(format string, args ...any) error {
	return &usageError{fmt.Errorf(format, args...)}
}

func isUsageError(err error) bool {
	var u *usageError
	if errors.As(err, &u) {
		return true
	}
	// "unknown command X for Y" comes from cobra's command resolution,
	// which has no error-func hook — match by prefix.
	return strings.HasPrefix(err.Error(), "unknown command")
}

// usageArgs wraps a cobra positional-args validator so failures classify
// as usage errors (ExitUsage).
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := v(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "faultkit",
		Short:         "Fault injection toolkit for AI/agent stacks",
		Long:          "faultkit deterministically triggers the failures your services\nhit in production, so you can fix them before your users do.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err}
	})
	root.AddCommand(newVersionCmd(), newScenarioCmd(), newRunCmd(), newCheckCmd(), newValidateCmd())
	return root
}
