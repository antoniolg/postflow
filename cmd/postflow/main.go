package main

import (
	"context"
	"os"

	"github.com/antoniolg/postflow/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
