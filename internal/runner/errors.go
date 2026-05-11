// Package runner manages the target process faultkit wraps.
package runner

import (
	"errors"
	"fmt"
)

// TargetExitError reports that the wrapped target process exited with a
// non-zero status.
type TargetExitError struct {
	ExitCode int
}

func (e *TargetExitError) Error() string {
	return fmt.Sprintf("target exited with status %d", e.ExitCode)
}

// ErrFaultNotFired reports that the target ran to completion without
// any matching fault firing.
var ErrFaultNotFired = errors.New("fault never fired during target run")
