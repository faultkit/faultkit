package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed anthropic_refusal.yaml
var anthropicRefusalYAML []byte

func init() {
	scenario.RegisterBuiltin("anthropic-refusal", anthropicRefusalYAML)
}
