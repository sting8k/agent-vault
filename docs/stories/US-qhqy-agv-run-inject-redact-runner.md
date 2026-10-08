# US-qhqy agv run: inject, redact, runner

<!-- Status, lane, verify command, and proof live in .harness/stories/US-qhqy.json.
     Read them with `just-harness-cli query stories`. Do not copy them here. -->

## Goal

`agv run [flags] -- CMD ARGS...` resolves placeholders and env flags against a secret source, runs the
child with secrets injected, redacts its output, and cleans up, as specified in `docs/design.md`
(Injection, Temp files, Run lifecycle, Redaction).

## Acceptance

- Placeholders follow the design grammar (`{{.ID}}` and other braces pass through), are substituted in
  one pass (values are never re-expanded), and are refused in `CMD`. A NUL byte in a value used as an
  arg or env var is an error naming `NAME.field` only.
- `--env` and `--env-from` override inherited vars; two flags setting the same var is an error. File
  fields become 0600 files in a per-run 0700 directory under an agv-owned root outside the workspace.
  A failed resolution leaves no temp files and starts nothing. Run directories of dead pids are swept.
- Child stdout and stderr are redacted per stream for every encoding in the design (not hex), across
  chunk boundaries; only a possible pattern prefix is held back; values over 1 MiB are refused.
- Lifecycle: SIGKILL of the process group leaves no child; one SIGINT to the group reaches the child
  once; SIGTERM/SIGHUP are forwarded; `--timeout` sends TERM, then KILL after about 2 s, and returns
  within a bound even when a grandchild holds the pipes (exit 124); `| head` ends the run cleanly
  (agv is not killed by SIGPIPE) and removes the temp directory.
- Exit codes: child's code, 124, 125, 126, 127, 128+N. Shells as `CMD` are refused without
  `--allow-shell`.
- No secret value reaches agv's own stdout, stderr or error messages (G-7n9t).

## Scope

- In: `internal/inject`, `internal/redact`, `internal/runner`, `internal/cli/run.go` and its tests.
- Out: the vault-backed `Source` (US-ewhv wires it by replacing `openSource` in `run.go`), `cli.go`,
  `design.md`, every other package.

## Proof

Verify command (see the story record): `go vet ./... && go test -race` over inject, redact, runner and
cli. The lifecycle criteria are tests in `internal/cli/run_lifecycle_test.go` that run agv as a real
process in its own process group; they re-exec the test binary as agv and as the child.

## Handoff

Implemented; `story verify` passes on Linux only. Not run on macOS or any other OS. The code uses
portable Unix calls (`kill`, `Setpgid`, `signal.Notify`, `WaitStatus`); `/proc` appears only in a test
helper, with a fallback. Status is left for the integrating actor to move to `implemented`.

SIGPIPE was checked, not assumed (Go 1.27, Linux): with no handler, a write to a closed stdout kills
agv with status 141; with `signal.Notify(SIGPIPE)` the write returns EPIPE and agv finishes. agv
uses Notify, not Ignore, because an ignored signal is inherited by the child across exec, which would
turn the child's `yes | head`-style SIGPIPE exit into a write error.

Open:
- `openSource` in `run.go` is a stub until the vault adapter is wired (US-ewhv).
- SIGQUIT is not handled: agv dies with a Go stack dump and the next start sweeps its temp dir.
- A signal that arrives while `inject.Build` runs, before `runner.Run` subscribes, kills agv the same
  way (temp dir swept on the next start).
- The stale sweep trusts `kill(pid, 0)`; it misjudges pids from another pid namespace that shares
  the root.
- Once, in a full `go test -race ./internal/cli/` run, the test binary reported `signal: terminated`
  during the timeout test. It did not recur in more than 20 full runs. Cause unknown.
