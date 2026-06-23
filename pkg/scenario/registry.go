// SPDX-License-Identifier: Apache-2.0

// Package scenario: registry resolution.
//
// The Resolver type encapsulates the search-path order the faultkit CLI
// uses to turn a user-provided scenario name into a loaded *Scenario.
// The order is:
//
//  1. Filesystem path (caller-provided, used as-is).
//  2. Registered builtin (always wins over the registry).
//  3. Registry root (when set):
//     a. <root>/<name>.yaml
//     b. <root>/<name>/scenario.yaml
//
// The Resolver takes only a path string. It does not know about any
// specific registry, the faultkit-scenarios repo, or the Pro repo. That
// is the OSS/Pro seam the project's CLAUDE.md requires.
package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolver turns a scenario name into a loaded Scenario using a fixed
// search order. The zero value is usable; an empty RegistryRoot
// disables the registry tier (only path and builtin are searched).
type Resolver struct {
	// RegistryRoot is the local directory faultkit searches for
	// user-authored scenarios. Empty disables the registry tier.
	// Trailing slashes are tolerated.
	RegistryRoot string
}

// NewResolver returns a Resolver rooted at root. An empty root is
// allowed and disables the registry tier.
func NewResolver(root string) *Resolver {
	return &Resolver{RegistryRoot: strings.TrimRight(root, string(filepath.Separator))}
}

// Resolve looks up name in the resolver's search order and returns the
// loaded Scenario. A miss returns a *UsageError — the CLI maps that to
// ExitUsage (4), matching how an unknown flag or unknown subcommand is
// reported today.
func (r *Resolver) Resolve(name string) (*Scenario, error) {
	// Tier 1: filesystem path. A name with a path separator or a
	// .yaml extension is treated as a path the user gave us. We do
	// not search the registry behind a path — that is the user's
	// explicit choice.
	if looksLikePath(name) {
		s, err := Load(name)
		if err != nil {
			return nil, &UsageError{err: fmt.Errorf("loading scenario %s: %w", name, err)}
		}
		return s, nil
	}

	// Tier 2: builtin. Builtins always win, even if a registry
	// file shadows the same name. This matches the principle that
	// the binary's own catalog is authoritative; a registry entry
	// that wants the same name needs to be renamed.
	if b, ok := builtins[name]; ok {
		return b.parsed, nil
	}

	// Tier 3: registry root. Empty root = no registry tier.
	if r.RegistryRoot == "" {
		return nil, &UsageError{err: fmt.Errorf(
			"scenario %q not found; checked built-ins and --registry-root (unset)", name)}
	}

	for _, candidate := range r.registryCandidates(name) {
		if _, err := os.Stat(candidate); err == nil {
			s, err := Load(candidate)
			if err != nil {
				return nil, &UsageError{err: fmt.Errorf("loading registry scenario %s: %w", candidate, err)}
			}
			return s, nil
		}
	}
	return nil, &UsageError{err: fmt.Errorf(
		"scenario %q not found; checked built-ins and --registry-root %q", name, r.RegistryRoot)}
}

// registryCandidates returns the on-disk paths to try, in order, for a
// non-path, non-builtin name. The two shapes are single-file and
// pack-style, both described in the registry spec.
func (r *Resolver) registryCandidates(name string) []string {
	cleaned := filepath.Clean(name)
	return []string{
		filepath.Join(r.RegistryRoot, cleaned+".yaml"),
		filepath.Join(r.RegistryRoot, cleaned, "scenario.yaml"),
	}
}

// looksLikePath reports whether name should be treated as a filesystem
// path. A name ending in .yaml is a path the user gave us; everything
// else is a logical name (which may itself contain a slash as the
// <pack>/<scenario> separator for registry lookups).
func looksLikePath(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".yaml")
}
