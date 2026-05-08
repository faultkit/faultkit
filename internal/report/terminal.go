package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteTerminal renders s as a short human-readable summary to w.
func WriteTerminal(w io.Writer, s Summary) error {
	fired := s.FiredCount()
	statusLabel := "PASS"
	if s.TargetExit != 0 {
		statusLabel = "FAIL"
	}

	if _, err := fmt.Fprintln(w, "=== faultkit summary ==="); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "scenario:     %s\n", s.Scenario); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "target:       %s\n", strings.Join(s.Target, " ")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "duration:     %s\n", s.Duration.Round(time.Millisecond)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "faults fired: %d\n", fired); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "target exit:  %d (%s)\n", s.TargetExit, statusLabel); err != nil {
		return err
	}
	return nil
}
