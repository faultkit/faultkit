package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/pkg/scenario"
)

func newScenarioListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List builtin scenarios",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			names := scenario.BuiltinNames()
			if len(names) == 0 {
				fmt.Fprintln(out, "(no builtin scenarios registered)")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, name := range names {
				s, err := scenario.LoadBuiltin(name)
				if err != nil {
					return fmt.Errorf("listing builtin %q: %w", name, err)
				}
				mode := scenarioMode(s)
				if mode == "" {
					mode = "?"
				}
				if s.Description == "" {
					fmt.Fprintf(tw, "%s\t[%s]\n", s.Name, mode)
				} else {
					fmt.Fprintf(tw, "%s\t[%s]\t%s\n", s.Name, mode, s.Description)
				}
			}
			return tw.Flush()
		},
	}
}
