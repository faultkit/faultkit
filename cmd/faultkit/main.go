package main

import (
	"os"

	"github.com/faultkit-dev/faultkit/internal/cli"

	_ "github.com/faultkit-dev/faultkit/internal/scenario/builtin"
)

func main() {
	os.Exit(cli.Execute())
}
