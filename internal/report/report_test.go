package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/report"
)

func sample() report.Summary {
	return report.Summary{
		Scenario:   "llm-api-degraded",
		Target:     []string{"curl", "https://api.openai.com/v1/models"},
		Duration:   1234 * time.Millisecond,
		TargetExit: 1,
		Events: []inject.Event{
			{Experiment: "openai-rate-limited", Fired: true},
			{Experiment: "openai-rate-limited", Fired: true},
		},
	}
}

func TestFiredCount(t *testing.T) {
	s := sample()
	if got := s.FiredCount(); got != 2 {
		t.Errorf("FiredCount = %d, want 2", got)
	}
	s.Events = append(s.Events, inject.Event{Fired: false})
	if got := s.FiredCount(); got != 2 {
		t.Errorf("FiredCount = %d, want 2 (non-fired event ignored)", got)
	}
}

func TestWriteTerminal(t *testing.T) {
	var buf bytes.Buffer
	report.WriteTerminal(&buf, sample())
	out := buf.String()
	wants := []string{
		"faultkit summary",
		"llm-api-degraded",
		"curl https://api.openai.com/v1/models",
		"1.234s",
		"faults fired: 2",
		"target exit:  1 (FAIL)",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n%s", w, out)
		}
	}
}

func TestWriteTerminalPassLabel(t *testing.T) {
	s := sample()
	s.TargetExit = 0
	var buf bytes.Buffer
	report.WriteTerminal(&buf, s)
	if !strings.Contains(buf.String(), "(PASS)") {
		t.Errorf("output should contain (PASS) when TargetExit=0:\n%s", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf, sample()); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var parsed report.Summary
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if parsed.Scenario != "llm-api-degraded" {
		t.Errorf("scenario = %q, want llm-api-degraded", parsed.Scenario)
	}
	if len(parsed.Events) != 2 {
		t.Errorf("events = %d, want 2", len(parsed.Events))
	}
}

// report/v1: WriteJSON stamps the schema id and a verdict derived from the
// same facts as the exit code (fired count + target exit).
func TestWriteJSONSchemaAndVerdict(t *testing.T) {
	cases := []struct {
		name      string
		fired     int
		exit      int
		wantState string
	}{
		{"no fault fired", 0, 0, "invalid_evidence"},
		{"no fault fired, target failed", 0, 1, "invalid_evidence"},
		{"fired, target failed", 2, 1, "silent_failure_confirmed"},
		{"fired, target held", 2, 0, "invariant_proven_under_fault"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := report.Summary{Scenario: "x", TargetExit: c.exit}
			for i := 0; i < c.fired; i++ {
				s.Events = append(s.Events, inject.Event{Fired: true})
			}
			var buf bytes.Buffer
			if err := report.WriteJSON(&buf, s); err != nil {
				t.Fatalf("WriteJSON: %v", err)
			}
			var out struct {
				Schema  string `json:"schema"`
				Verdict struct {
					State  string `json:"state"`
					Reason string `json:"reason"`
				} `json:"verdict"`
			}
			if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
			}
			if out.Schema != "faultkit.dev/report/v1" {
				t.Errorf("schema = %q, want faultkit.dev/report/v1", out.Schema)
			}
			if out.Verdict.State != c.wantState {
				t.Errorf("verdict.state = %q, want %q", out.Verdict.State, c.wantState)
			}
			if out.Verdict.Reason == "" {
				t.Error("verdict.reason should not be empty")
			}
		})
	}
}
