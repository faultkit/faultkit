package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed llm_api_degraded.yaml
var llmAPIDegradedYAML []byte

func init() {
	scenario.RegisterBuiltin("llm-api-degraded", llmAPIDegradedYAML)
}
