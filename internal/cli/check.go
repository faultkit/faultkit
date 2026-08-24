package cli

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
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

	fmt.Fprintln(out, "\nproviders:")
	pw := tabwriter.NewWriter(out, 0, 0, 1, ' ', 0)
	for _, id := range proxy.ProviderIDs() {
		var modes []string
		for _, m := range fixtures.Modes() {
			if _, ok := fixtures.For(m, id); ok {
				modes = append(modes, m)
			}
		}
		fmt.Fprintf(pw, "  %s\t%s\n", id, strings.Join(modes, ", "))
	}
	if err := pw.Flush(); err != nil {
		return err
	}

	if !anyAvailable {
		return errors.New("no fault-injection modes available on this host")
	}
	return nil
}
