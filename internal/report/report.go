// Package report formats faultkit run results for humans (terminal)
// and machines (JSON for CI consumption).
package report

import (
	"time"

	"github.com/faultkit/faultkit/internal/inject"
)

// Summary is the user-facing record of a faultkit run.
type Summary struct {
	Scenario   string         `json:"scenario"`
	Target     []string       `json:"target"`
	Duration   time.Duration  `json:"duration_ns"`
	TargetExit int            `json:"target_exit"`
	Events     []inject.Event `json:"events"`
}

// FiredCount returns the number of fault events whose Fired field is true.
func (s Summary) FiredCount() int {
	n := 0
	for _, e := range s.Events {
		if e.Fired {
			n++
		}
	}
	return n
}
