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
