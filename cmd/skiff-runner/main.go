package main

import (
	"os"

	"github.com/s1liconcow/skiff/internal/cli"
)

func main() {
	os.Exit(cli.Run("skiff-runner", os.Args[1:], os.Stdout, os.Stderr))
}
