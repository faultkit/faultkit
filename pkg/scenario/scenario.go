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

// Experiment is one fault rule within a Scenario.
type Experiment struct {
	Name        string           `yaml:"name"`
	Fault       faulttypes.Fault `yaml:"fault"`
	Match       Match            `yaml:"match"`
	Probability float64          `yaml:"probability"`
}

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
