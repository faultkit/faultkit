package main

import (
	"os"

	"github.com/faultkit/faultkit/internal/cli"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
)

func main() {
	os.Exit(cli.Execute())
}
