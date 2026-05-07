package cli

import "github.com/spf13/cobra"

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] -- <target> [target args...]",
		Short: "Run a target command under fault injection",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errNotImpl("run")
		},
	}
	cmd.Flags().String("scenario", "", "builtin scenario name")
	cmd.Flags().String("config", "", "scenario YAML file")
	cmd.Flags().String("mode", "auto", "injection mode: auto, proxy, ebpf")
	cmd.Flags().String("report", "", "write JSON report to path")
	cmd.Flags().Bool("verbose", false, "log every fault decision")
	return cmd
}
