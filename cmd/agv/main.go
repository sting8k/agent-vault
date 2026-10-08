package main

import (
	"os"

	"github.com/sting8k/agent-vault/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
