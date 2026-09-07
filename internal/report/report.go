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

// SchemaV1 identifies the machine-readable report format emitted by WriteJSON.
// A change to the shape of that JSON is a change to this identifier.
const SchemaV1 = "faultkit.dev/report/v1"

// Summary is the user-facing record of a faultkit run.
type Summary struct {
	Scenario   string         `json:"scenario"`
	Target     []string       `json:"target"`
	Duration   time.Duration  `json:"duration_ns"`
	TargetExit int            `json:"target_exit"`
	Events     []inject.Event `json:"events"`
}

// Verdict is the proof state of a run. It is derived from the same facts as
// the exit code (fired count + target exit), so the JSON verdict and the
// process exit code can never disagree.
type Verdict struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// Proof states carried by Verdict.State.
const (
	verdictInvalidEvidence = "invalid_evidence"
	verdictSilentFailure   = "silent_failure_confirmed"
	verdictProven          = "invariant_proven_under_fault"
)

// verdictFor derives the proof state from a summary.
func verdictFor(s Summary) Verdict {
	switch {
	case s.FiredCount() == 0:
		return Verdict{verdictInvalidEvidence, "no fault fired; the run proves nothing"}
	case s.TargetExit != 0:
		return Verdict{verdictSilentFailure, fmt.Sprintf("fault fired and the target failed (exit %d)", s.TargetExit)}
	default:
		return Verdict{verdictProven, "fault fired and the target held (exit 0)"}
	}
}

// jsonReport is the wire shape of report/v1: the summary fields, stamped with
// the schema id and the derived verdict.
type jsonReport struct {
	Schema string `json:"schema"`
	Summary
	Verdict Verdict `json:"verdict"`
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

// WriteJSON renders s as an indented report/v1 JSON document to w, stamping
// the schema id and the derived verdict.
func WriteJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonReport{Schema: SchemaV1, Summary: s, Verdict: verdictFor(s)})
}
