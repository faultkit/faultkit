package cli

import "github.com/spf13/cobra"

func newScenarioCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Inspect builtin and user scenarios",
		Args:  usageArgs(cobra.NoArgs),
	}
	cmd.AddCommand(newScenarioListCmd(), newScenarioShowCmd())
	return cmd
}
