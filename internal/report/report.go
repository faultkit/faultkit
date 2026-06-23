// Package report formats faultkit run results for humans (terminal)
// and machines (JSON for CI consumption).
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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

// WriteTerminal renders s as a short human-readable summary to w.
func WriteTerminal(w io.Writer, s Summary) {
	statusLabel := "PASS"
	if s.TargetExit != 0 {
		statusLabel = "FAIL"
	}
	fmt.Fprintf(w, "=== faultkit summary ===\n"+
		"scenario:     %s\n"+
		"target:       %s\n"+
		"duration:     %s\n"+
		"faults fired: %d\n"+
		"target exit:  %d (%s)\n",
		s.Scenario,
		strings.Join(s.Target, " "),
		s.Duration.Round(time.Millisecond),
		s.FiredCount(),
		s.TargetExit, statusLabel)
}

// WriteJSON renders s as indented JSON to w.
func WriteJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}
