package main

import (
	"os"

	"github.com/fluxinc/our-ai/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args))
}
