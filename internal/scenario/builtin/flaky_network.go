package builtin

import (
	_ "embed"

	"github.com/faultkit/faultkit/pkg/scenario"
)

//go:embed flaky_network.yaml
var flakyNetworkYAML []byte

func init() {
	scenario.RegisterBuiltin("flaky-network", flakyNetworkYAML)
}
