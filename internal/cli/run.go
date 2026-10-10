package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/sting8k/agent-vault/internal/audit"
	"github.com/sting8k/agent-vault/internal/inject"
	"github.com/sting8k/agent-vault/internal/runner"
)

// openSource opens the secret store a run reads from; tests set a fake.
var openSource = openVaultSource

const runUsage = `usage: agv run [flags] -- CMD ARGS...

Runs CMD with secrets injected and its output redacted. Quote placeholders so
your shell does not touch them: '{{NAME}}', '{{NAME.field}}', '{{file:NAME}}'.

Flags:
  --env VAR=TEMPLATE   set VAR to TEMPLATE after substitution (repeatable)
  --env-from NAME      set every field of NAME that has an env name (repeatable)
  --timeout DURATION   stop CMD after DURATION, for example 30s (default: none)
  --allow-shell        allow CMD to be a shell
`

func cmdRun(args []string, io IO) int {
	a, err := parseRun(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(io.Stdout, runUsage)
		return 0
	}
	if err != nil {
		return fail(io, err)
	}
	tr, err := openTrail(io)
	if err != nil {
		return fail(io, err)
	}
	src, err := openSource(io)
	if err != nil {
		return fail(io, err)
	}
	plan, err := inject.Build(inject.Request{Argv: a.argv, Env: a.env, EnvFrom: a.envFrom, Environ: io.Env}, src)
	if err != nil {
		return fail(io, err)
	}
	// The audit line is written from Finished, while runner still holds the signal handlers, so a
	// signal or timeout that ends the child is logged too. Webhooks wait until Run has returned:
	// Ctrl-C may cut them short, and they never change the exit code.
	var rec audit.Record
	tr.start = time.Now()
	code, err := runner.Run(plan, runner.Options{
		Stdin: io.Stdin, Stdout: io.Stdout, Stderr: io.Stderr,
		Timeout: a.timeout, AllowShell: a.allowShell,
		Finished: func(code int) {
			labels := make([]string, len(plan.Secrets))
			for i, s := range plan.Secrets {
				labels[i] = s.Label
			}
			rec = tr.record(audit.ActionRun, labels, plan.Argv[0], code)
			tr.log(rec)
		},
	})
	if err != nil {
		fmt.Fprintf(io.Stderr, "agv: %v\n", err)
	}
	tr.notify(rec)
	return code
}

type runArgs struct {
	env, envFrom stringList
	timeout      time.Duration
	allowShell   bool
	argv         []string // CMD ARGS..., after "--"
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// parseRun splits args at the first "--": flags before it, the command after
// it. Nothing after "--" is parsed, so the command keeps its own flags.
func parseRun(args []string) (runArgs, error) {
	var a runArgs
	sep := slices.Index(args, "--")
	if sep < 0 {
		if slices.ContainsFunc(args, func(s string) bool { return s == "-h" || s == "-help" || s == "--help" }) {
			return a, flag.ErrHelp
		}
		return a, errors.New("missing '--' before the command; usage: agv run [flags] -- CMD ARGS...")
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&a.env, "env", "")
	fs.Var(&a.envFrom, "env-from", "")
	fs.DurationVar(&a.timeout, "timeout", 0, "")
	fs.BoolVar(&a.allowShell, "allow-shell", false, "")
	if err := fs.Parse(args[:sep]); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q before '--'", fs.Arg(0))
	}
	if a.timeout < 0 {
		return a, errors.New("--timeout must not be negative")
	}
	if a.argv = args[sep+1:]; len(a.argv) == 0 {
		return a, errors.New("no command after '--'")
	}
	return a, nil
}
