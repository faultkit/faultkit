package scenario

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// Load reads and validates a scenario from a YAML file at path.
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading scenario %s: %w", path, err)
	}
	s, err := LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("loading scenario %s: %w", path, err)
	}
	return s, nil
}

// LoadBytes parses and validates a scenario from a YAML byte slice.
func LoadBytes(data []byte) (*Scenario, error) {
	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing scenario yaml: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

type builtin struct {
	raw    []byte
	parsed *Scenario
}

// builtins is populated at init() time only; reads after init are
// lock-free.
var builtins = map[string]builtin{}

// RegisterBuiltin registers a YAML blob under name as a builtin scenario.
// Called from internal/scenario/builtin init() functions. Panics if the
// YAML is invalid — that's a build-time bug, not a runtime condition.
func RegisterBuiltin(name string, data []byte) {
	if _, exists := builtins[name]; exists {
		panic(fmt.Sprintf("scenario.RegisterBuiltin: duplicate name %q", name))
	}
	s, err := LoadBytes(data)
	if err != nil {
		panic(fmt.Sprintf("scenario.RegisterBuiltin: invalid builtin %q: %v", name, err))
	}
	builtins[name] = builtin{raw: data, parsed: s}
}

// LoadBuiltin returns a registered builtin scenario by name.
func LoadBuiltin(name string) (*Scenario, error) {
	b, ok := builtins[name]
	if !ok {
		return nil, fmt.Errorf("unknown builtin scenario %q", name)
	}
	return b.parsed, nil
}

// BuiltinNames returns the sorted list of registered builtin scenario names.
func BuiltinNames() []string {
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// BuiltinYAML returns the embedded YAML bytes for a registered builtin.
// `faultkit scenario show` uses this so the printed output preserves the
// original formatting and comments rather than re-marshaling.
func BuiltinYAML(name string) ([]byte, error) {
	b, ok := builtins[name]
	if !ok {
		return nil, fmt.Errorf("unknown builtin scenario %q", name)
	}
	return b.raw, nil
}
