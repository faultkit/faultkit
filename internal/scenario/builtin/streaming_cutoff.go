package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed streaming_cutoff.yaml
var streamingCutoffYAML []byte

func init() {
	scenario.RegisterBuiltin("llm-streaming-cutoff", streamingCutoffYAML)
}
