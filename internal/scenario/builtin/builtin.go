// Package builtin registers the scenarios shipped with the faultkit binary.
// Every *.yaml in this directory is embedded and registered under its own
// `name:` field at init — adding a scenario is just dropping in a YAML file.
package builtin

import (
	"embed"
	"fmt"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed *.yaml
var scenarioFS embed.FS

func init() {
	entries, err := scenarioFS.ReadDir(".")
	if err != nil {
		panic(fmt.Sprintf("builtin: read embedded scenarios: %v", err))
	}
	for _, e := range entries {
		data, err := scenarioFS.ReadFile(e.Name())
		if err != nil {
			panic(fmt.Sprintf("builtin: read %s: %v", e.Name(), err))
		}
		s, err := scenario.LoadBytes(data)
		if err != nil {
			panic(fmt.Sprintf("builtin: invalid %s: %v", e.Name(), err))
		}
		scenario.RegisterBuiltin(s.Name, data)
	}
}
