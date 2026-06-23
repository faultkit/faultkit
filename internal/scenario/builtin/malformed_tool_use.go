package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed malformed_tool_use.yaml
var malformedToolUseYAML []byte

func init() {
	scenario.RegisterBuiltin("malformed-tool-use", malformedToolUseYAML)
}
