package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed anthropic_tool_use_cutoff.yaml
var anthropicToolUseCutoffYAML []byte

func init() {
	scenario.RegisterBuiltin("anthropic-tool-use-cutoff", anthropicToolUseCutoffYAML)
}
