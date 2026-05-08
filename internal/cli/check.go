package cli

import (
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/faultkit-dev/faultkit/internal/inject"
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Report which faultkit modes are available on this host",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheck(cmd.OutOrStdout())
		},
	}
}

func runCheck(out io.Writer) error {
	if _, err := fmt.Fprintf(out, "platform:    %s/%s\n", runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}

	reports := inject.AvailableModes()
	width := modeColumnWidth(reports)
	anyAvailable := false
	for _, r := range reports {
		if r.Available {
			anyAvailable = true
			line := fmt.Sprintf("%-*s mode: ok", width, r.Mode)
			if r.Reason != "" {
				line += " " + r.Reason
			}
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "%-*s mode: unavailable — %s\n", width, r.Mode, r.Reason); err != nil {
			return err
		}
	}

	if !anyAvailable {
		return errors.New("no fault-injection modes available on this host")
	}
	return nil
}

func modeColumnWidth(reports []inject.ModeReport) int {
	width := 0
	for _, r := range reports {
		if l := len(r.Mode); l > width {
			width = l
		}
	}
	return width
}
