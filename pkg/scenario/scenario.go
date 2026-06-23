// Package scenario defines the user-facing scenario model: a Scenario
// carries one or more Experiments, each with a Fault and a Match rule.
package scenario

import (
	"errors"
	"fmt"

	"github.com/faultkit/faultkit/pkg/faulttypes"
)

// Scenario is the top-level YAML object loaded from a scenario file.
type Scenario struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description,omitempty"`
	Requires    Requires     `yaml:"requires,omitempty"`
	Experiments []Experiment `yaml:"experiments"`
}

// Experiment is one fault rule within a Scenario. It is expressed in one
// of two forms: fixture-driven (Failure names a provider-agnostic failure
// mode, optionally narrowed to one Provider) or raw (an explicit Fault and
// Match). The two forms are mutually exclusive.
type Experiment struct {
	Name string `yaml:"name"`

	// Failure names a failure mode resolved against the per-provider fixture
	// catalog at injection time. When set, Fault/Match must be empty.
	Failure string `yaml:"failure,omitempty"`
	// Provider narrows a fixture-driven experiment to a single provider id
	// (e.g. "anthropic"). Empty means every provider that has a fixture for
	// Failure. Only valid alongside Failure.
	Provider string `yaml:"provider,omitempty"`

	Fault       faulttypes.Fault `yaml:"fault,omitempty"`
	Match       Match            `yaml:"match,omitempty"`
	Probability float64          `yaml:"probability"`
}

// IsFixtureDriven reports whether the experiment names a failure mode
// (resolved via the fixture catalog) rather than carrying a raw fault.
func (e Experiment) IsFixtureDriven() bool { return e.Failure != "" }

var (
	ErrExperimentMixed      = errors.New("experiment sets both a failure mode and a raw fault/match")
	ErrProviderNeedsFailure = errors.New("provider is set without a failure mode")
)

// Match describes which traffic an Experiment applies to.
type Match struct {
	Host    string `yaml:"host,omitempty"`
	Path    string `yaml:"path,omitempty"`
	Syscall string `yaml:"syscall,omitempty"`
}

func (m Match) IsHTTP() bool    { return m.Host != "" || m.Path != "" }
func (m Match) IsSyscall() bool { return m.Syscall != "" }

var (
	ErrMatchMixed = errors.New("match mixes HTTP-level and syscall-level fields")
	ErrMatchEmpty = errors.New("match is empty: set host, path, or syscall")
)

func (m Match) Validate() error {
	if m.IsHTTP() && m.IsSyscall() {
		return ErrMatchMixed
	}
	if !m.IsHTTP() && !m.IsSyscall() {
		return ErrMatchEmpty
	}
	return nil
}

// Requires expresses platform-level prerequisites for a Scenario.
type Requires struct {
	Platform  string `yaml:"platform,omitempty"`
	KernelMin string `yaml:"kernel_min,omitempty"`
}

// Validate reports whether s is a coherent Scenario.
func (s *Scenario) Validate() error {
	if !isKebabCase(s.Name) {
		return fmt.Errorf("scenario name %q is not kebab-case", s.Name)
	}
	if len(s.Experiments) == 0 {
		return fmt.Errorf("scenario %q has no experiments", s.Name)
	}
	for _, exp := range s.Experiments {
		if exp.Name == "" {
			return fmt.Errorf("scenario %q: experiment with no name", s.Name)
		}
		if exp.Probability < 0 || exp.Probability > 1 {
			return fmt.Errorf("scenario %q experiment %q: probability %v outside [0,1]", s.Name, exp.Name, exp.Probability)
		}
		if exp.IsFixtureDriven() {
			// Fixture-driven: the failure mode and provider are resolved
			// against the fixture catalog at injection time, so a raw
			// fault/match here is a contradiction. Mode/provider validity is
			// checked there (this package can't see the provider registry).
			if exp.Fault.IsHTTP() || exp.Fault.IsSyscall() || exp.Match.IsHTTP() || exp.Match.IsSyscall() {
				return fmt.Errorf("scenario %q experiment %q: %w", s.Name, exp.Name, ErrExperimentMixed)
			}
			continue
		}
		if exp.Provider != "" {
			return fmt.Errorf("scenario %q experiment %q: %w", s.Name, exp.Name, ErrProviderNeedsFailure)
		}
		if err := exp.Fault.Validate(); err != nil {
			return fmt.Errorf("scenario %q experiment %q: %w", s.Name, exp.Name, err)
		}
		if err := exp.Match.Validate(); err != nil {
			return fmt.Errorf("scenario %q experiment %q: %w", s.Name, exp.Name, err)
		}
		if exp.Fault.IsHTTP() != exp.Match.IsHTTP() {
			return fmt.Errorf("scenario %q experiment %q: fault and match must both be HTTP-level or both syscall-level", s.Name, exp.Name)
		}
	}
	return nil
}

func isKebabCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case (r >= '0' && r <= '9') && i > 0:
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}
