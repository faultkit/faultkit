// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/pkg/scenario"
)

func newScenarioListCmd() *cobra.Command {
	var registryRoot string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List builtin and registry scenarios",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("registry-root") {
				if env := os.Getenv("FAULTKIT_REGISTRY_ROOT"); env != "" {
					registryRoot = env
				}
			}
			out := cmd.OutOrStdout()

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			rows := 0

			// Builtins: unchanged shape, [proxy]/[ebpf] tag, no source tag.
			for _, name := range scenario.BuiltinNames() {
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
				rows++
			}

			// Registry: [registry] tag, mechanism derived from experiments.
			// Skipped silently when registryRoot is empty. A bad root
			// is a user mistake and surfaces as a usage error.
			if registryRoot != "" {
				regRows, err := listRegistry(registryRoot)
				if err != nil {
					return UsageErrorf("%w", err)
				}
				for _, r := range regRows {
					mode := r.mode
					if mode == "" {
						mode = "?"
					}
					if r.description == "" {
						fmt.Fprintf(tw, "%s\t[%s]\t[registry]\n", r.name, mode)
					} else {
						fmt.Fprintf(tw, "%s\t[%s]\t[registry]\t%s\n", r.name, mode, r.description)
					}
					rows++
				}
			}

			if rows == 0 {
				fmt.Fprintln(out, "(no scenarios found)")
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&registryRoot, "registry-root", "", "directory of a local scenario registry clone (env: FAULTKIT_REGISTRY_ROOT)")
	return cmd
}

type registryRow struct {
	name        string
	mode        string // "proxy" | "ebpf" | "?"
	description string
}

// listRegistry walks root, parses every <pack>/<name>.yaml and
// <pack>/<name>/scenario.yaml it finds, and returns one row per
// scenario. Scenarios that fail to parse are reported as a usage
// error so the user knows the registry is in a bad state.
func listRegistry(root string) ([]registryRow, error) {
	cleaned := strings.TrimRight(root, string(filepath.Separator))
	info, err := os.Stat(cleaned)
	if err != nil {
		return nil, fmt.Errorf("--registry-root %q: %w", cleaned, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--registry-root %q is not a directory", cleaned)
	}
	var rows []registryRow
	walkErr := filepath.WalkDir(cleaned, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		// Single-file shape: <pack>/<name>.yaml at the root or
		// one level deep (we keep the walk shallow by not recursing
		// past the first level below a pack directory).
		if base == "scenario.yaml" || strings.HasSuffix(base, ".yaml") {
			s, lerr := scenario.Load(path)
			if lerr != nil {
				return fmt.Errorf("invalid registry scenario %s: %w", path, lerr)
			}
			rows = append(rows, registryRow{
				name:        s.Name,
				mode:        scenarioMode(s),
				description: s.Description,
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	// Stable, alphabetic order so the output is reproducible.
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows, nil
}
