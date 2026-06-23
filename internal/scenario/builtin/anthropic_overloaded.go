package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed anthropic_overloaded.yaml
var anthropicOverloadedYAML []byte

func init() {
	scenario.RegisterBuiltin("anthropic-overloaded", anthropicOverloadedYAML)
}
