package main

import (
	"os"

	"github.com/sting8k/agent-vault/internal/cli"
)

// version is stamped at release time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
