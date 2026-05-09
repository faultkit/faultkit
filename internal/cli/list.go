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
			loaded := make([]*scenario.Scenario, 0, len(names))
			nameWidth := 0
			for _, name := range names {
				s, err := scenario.LoadBuiltin(name)
				if err != nil {
					return fmt.Errorf("listing builtin %q: %w", name, err)
				}
				loaded = append(loaded, s)
				if len(name) > nameWidth {
					nameWidth = len(name)
				}
			}
			for _, s := range loaded {
				mode := scenarioMode(s)
				if mode == "" {
					mode = "?"
				}
				if s.Description == "" {
					fmt.Fprintf(out, "%-*s  [%s]\n", nameWidth, s.Name, mode)
				} else {
					fmt.Fprintf(out, "%-*s  [%s]  %s\n", nameWidth, s.Name, mode, s.Description)
				}
			}
			return nil
		},
	}
}
