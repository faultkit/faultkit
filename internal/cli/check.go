package cli

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/internal/inject"
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
	fmt.Fprintf(out, "platform:    %s/%s\n", runtime.GOOS, runtime.GOARCH)

	tw := tabwriter.NewWriter(out, 0, 0, 1, ' ', 0)
	anyAvailable := false
	for _, r := range inject.AvailableModes() {
		if r.Available {
			anyAvailable = true
			status := "mode: ok"
			if r.Reason != "" {
				status += " " + r.Reason
			}
			fmt.Fprintf(tw, "%s\t%s\n", r.Mode, status)
			continue
		}
		fmt.Fprintf(tw, "%s\tmode: unavailable — %s\n", r.Mode, r.Reason)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if !anyAvailable {
		return errors.New("no fault-injection modes available on this host")
	}
	return nil
}
