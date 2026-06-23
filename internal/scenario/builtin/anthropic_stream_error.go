package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed anthropic_stream_error.yaml
var anthropicStreamErrorYAML []byte

func init() {
	scenario.RegisterBuiltin("anthropic-stream-error", anthropicStreamErrorYAML)
}
