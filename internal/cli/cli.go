// Package cli dispatches agv commands and owns all user-facing text.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// IO is what every command gets: streams and the process environment.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    []string // os.Environ() form; read AGV_HOME etc. from here, never os.Getenv
}

// errNotImplemented marks a command whose scope is not built yet.
var errNotImplemented = errors.New("not implemented")

type command func(args []string, io IO) int

// Each command lives in its own file so separate scopes do not edit the same file.
var commands = map[string]command{
	"set":    cmdSet,    // set.go
	"list":   cmdList,   // list.go
	"rm":     cmdRm,     // rm.go
	"run":    cmdRun,    // run.go
	"skills": cmdSkills, // skills.go
	"skill":  cmdSkills,
}

// Main runs agv with args (without the program name) and returns the exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	io := IO{Stdin: stdin, Stdout: stdout, Stderr: stderr, Env: env}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fail(io, fmt.Errorf("unknown command %q; run 'agv help'", args[0]))
	}
	return cmd(args[1:], io)
}

// fail prints an agv error (always prefixed "agv: ") and returns exit code 125.
// The message must never contain a secret value (guardrail G-7n9t).
func fail(io IO, err error) int {
	fmt.Fprintf(io.Stderr, "agv: %v\n", err)
	return 125
}

const usage = `agv - let coding agents use local secrets without seeing them

Agent commands:
  agv skills                 how to use agv (read this first)
  agv list [filter] [--json] secret names, descriptions and fields (never values)
  agv run [flags] -- CMD ARGS...
                             run CMD with secrets injected and output redacted

Human commands (run in your own terminal):
  agv set [NAME]             add or update a secret
  agv rm NAME                remove a secret
`
