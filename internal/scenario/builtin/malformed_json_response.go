package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed malformed_json_response.yaml
var malformedJSONResponseYAML []byte

func init() {
	scenario.RegisterBuiltin("malformed-json-response", malformedJSONResponseYAML)
}
