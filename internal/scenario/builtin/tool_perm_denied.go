package builtin

import (
	_ "embed"

	"github.com/faultkit-dev/faultkit/pkg/scenario"
)

//go:embed tool_perm_denied.yaml
var toolPermDeniedYAML []byte

func init() {
	scenario.RegisterBuiltin("tool-permission-denied", toolPermDeniedYAML)
}
