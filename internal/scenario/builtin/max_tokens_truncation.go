package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed max_tokens_truncation.yaml
var maxTokensTruncationYAML []byte

func init() {
	scenario.RegisterBuiltin("max-tokens-truncation", maxTokensTruncationYAML)
}
