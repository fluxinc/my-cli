package main

import (
	"os"

	"github.com/fluxinc/my-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args))
}
