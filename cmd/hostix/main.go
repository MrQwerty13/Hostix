package main

import (
	"context"
	"fmt"
	"os"

	"github.com/MrQwerty13/Hostix/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
