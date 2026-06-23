package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed anthropic_request_too_large.yaml
var anthropicRequestTooLargeYAML []byte

func init() {
	scenario.RegisterBuiltin("anthropic-request-too-large", anthropicRequestTooLargeYAML)
}
