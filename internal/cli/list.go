package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/faultkit-dev/faultkit/pkg/scenario"
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
			for _, name := range names {
				s, err := scenario.LoadBuiltin(name)
				if err != nil {
					return fmt.Errorf("listing builtin %q: %w", name, err)
				}
				if s.Description == "" {
					fmt.Fprintln(out, name)
				} else {
					fmt.Fprintf(out, "%s — %s\n", name, s.Description)
				}
			}
			return nil
		},
	}
}
