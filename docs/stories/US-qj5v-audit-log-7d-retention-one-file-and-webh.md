# US-qj5v Audit log (7d retention, one file) and webhook notify (ntfy, json) configured by config.json

<!-- Status, lane, verify command, and proof live in .harness/stories/US-qj5v.json.
     Read them with `just-harness-cli query stories`. Do not copy them here. -->

## Goal

The user can see what agents did with their secrets: an audit log of `run`, `set` and `rm`, and
optional webhook notifications (ntfy, json), both set up by a hand-edited `$AGV_HOME/config.json`.
Contract: `docs/design.md`, "Audit log and webhooks".

## Acceptance

- `config.json` is optional; a missing file means the defaults; an invalid one is an error that
  names the file and stops `run`, `set` and `rm`. No command and no env var changes it.
- `audit.log` is one 0600 file, on by default. One JSON line per `run`, `set`, `rm` (not `list`):
  time, action, secret names, program (argv[0] only), cwd, exit code, duration. Never a value, never
  another argument. Entries older than the retention (default 7d) are dropped.
- Concurrent agv processes lose and corrupt no line, also while the file is being pruned. A failed
  write is `agv: audit log: ...` on stderr; the command still runs with its own exit code.
- A finished action that matches a webhook (actions AND secrets filter; empty matches all) is sent
  once at the end, within about 3 s. Failure is a stderr warning, never a failed command, never a
  changed exit code. `json` posts the record; `ntfy` posts a readable message (Title/Priority/Tags).
- A webhook URL may be `{{NAME}}`/`{{NAME.field}}`, resolved from the vault; the URL never appears
  in a warning or error.
- A timeout or a signal that ends the child is logged; SIGKILL of agv cannot be.

## Scope

- In: `internal/audit` (new), `internal/inject/expand.go`, `runner.Options.Finished`, the three
  call sites in `internal/cli` (`run.go`, `set.go`, `rm.go`, glue in `audit.go`), design.md, README,
  the agent skill.
- Out: vault storage, `list`, redaction, release/CHANGELOG (written at release time).

## Proof

Verify command: gofmt, vet, `go test -race ./...`. Risk to test: no value or extra argument in
`audit.log` or a webhook body; pruning keeps exactly the non-expired lines; concurrent writers lose
nothing while pruning; webhook trouble leaves the exit code and hides the URL; the filters; an
invalid config names the file; the record survives a timeout and SIGTERM; `Finished` runs shielded
from signals. Manual: a real ntfy.sh topic, read back with `curl .../json?poll=1`.

## Design

- **Record written from `runner.Options.Finished`**, a defer between temp-file cleanup and
  `signal.Stop`: the child is gone, temp files are removed, signals are still caught. Writing after
  `Run` returns would leave a window where SIGTERM kills agv before the line is written. Webhooks
  go after `Run` returns so Ctrl-C can cut a slow one short.
- **One file, pruned under `audit.lock`.** Pruning replaces the file by rename, so appenders must be
  excluded for the whole prune: flock on a separate lock file (not the log, whose inode changes;
  not the vault's `.lock`, so a big rewrite never delays `agv set`). Rewrite only when the first
  line has expired. Rejected: in-place truncate-and-rewrite (a crash loses the log), rotated
  copies (forbidden by the spec), a lazy sweep interval (an entry would outlive the retention).
- **Webhook URL is a template** expanded by `inject.Expand` (same grammar as `run`, no temp files).
  Warnings are fixed reasons; a transport error is never printed because it contains the URL.
- **Invalid config fails the command** (spec: do not ignore silently), checked before anything runs.

## Handoff

Implemented on `dev-local`; verify passes; real ntfy check done. Not logged by design: runs that fail
before a Plan exists, refused or cancelled `set`/`rm`. Not handled: a torn last line after a crash
or full disk. `json` format is rejected by Slack and Discord (need `text` / `content`).
