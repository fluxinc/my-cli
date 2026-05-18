package main

import (
	"os"

	"github.com/fluxinc/flux/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args))
}
