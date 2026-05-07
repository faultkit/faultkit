package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

func newScenarioShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a builtin scenario as YAML",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := scenario.BuiltinYAML(args[0])
			if err != nil {
				return &usageError{err}
			}
			fmt.Fprint(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
}
