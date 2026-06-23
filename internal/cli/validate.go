// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/pkg/scenario"
)

// newValidateCmd returns `faultkit validate <file>`. It parses and
// validates a single scenario YAML against the package's schema and
// exits 0 on success, ExitUsage on any user-visible failure (bad
// path, bad YAML, schema violation). The registry's CI shells out to
// this command pinned to a specific faultkit version, so the exit
// contract is part of the registry's public surface.
func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a scenario YAML against the faultkit schema",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			s, err := scenario.Load(path)
			if err != nil {
				return &usageError{fmt.Errorf("invalid scenario %s: %w", path, err)}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok: %s (%s)\n", s.Name, path)
			return nil
		},
	}
}
