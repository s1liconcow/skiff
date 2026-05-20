package main

import (
	"context"
	"os"

	"github.com/s1liconcow/skiff/internal/packages/postgresha"
)

func main() {
	os.Exit(postgresha.Execute(context.Background(), os.Stdin, os.Stdout, os.Stderr, postgresha.Options{}))
}
