package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version, commit, and date are set via -ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the faultkit version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if short {
				fmt.Fprintln(out, version)
				return nil
			}
			fmt.Fprintf(out, "faultkit %s\ncommit: %s\nbuilt:  %s\n", version, commit, date)
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version, no commit/date")
	return cmd
}
