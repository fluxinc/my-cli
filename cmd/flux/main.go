package main

import (
	"os"

	"github.com/fluxinc/flux-ai/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args))
}
