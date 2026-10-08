# Agent Instructions

Instructions for developing agv in this repo. How agents *use* agv lives in
`internal/skill/SKILL.md` (`agv skills`), not here.

## Project

- `docs/design.md` is the contract: guarantees, storage, injection, lifecycle, redaction, layout.
  Change it in the same commit when behaviour changes.
- Go, standard library plus `golang.org/x/term`; no other dependencies without a decision.
- Package roles and allowed imports are in design.md "Code layout". `inject` never imports `vault`;
  `cli` owns all user-facing text and the vault→`inject.Source` adapter (`cli/run_source.go`).
- Unix-only code goes in `_unix.go` files. Windows is not supported yet.

## Commands

```sh
export PATH=$HOME/.local/go/bin:$PATH          # if go is not on PATH
gofmt -l . && go vet ./... && go test -race ./...
go build -o /tmp/agv ./cmd/agv
AGV_HOME=$(mktemp -d) /tmp/agv ...             # manual runs; never touch the real ~/.agent-vault
```

## Rules

- A secret value never reaches agv's own output, errors, logs or argv (guardrail G-7n9t). Build
  messages from names and `NAME.field` labels only; do not echo user-supplied flag values or paths.
- Tests use `AGV_HOME` temp dirs and fake values. Test invariants and real risks, not wording.
  A bug fix comes with a test that fails on the old code.
- Lifecycle code (signals, process groups, temp files) must stay correct on both Linux and macOS;
  CI runs both.
- Work on branch `dev-local`. Never push or tag without the user's explicit approval.

<!-- HARNESS:BEGIN -->
## Harness

This repo keeps shared state for agents and people in `.harness/` (committed; single source of truth).

- At session start: run `.harness/bin/just-harness-cli query status` and follow `docs/HARNESS.md`.
- Active guardrails are binding. If a request conflicts with one, say so and ask.
- Write `.harness/` only through the CLI, never by hand. Prose goes in `docs/`.
- Most work needs no records. Create a story, decision, guardrail, or trace only when `docs/HARNESS.md` says it is needed.
- Do not claim work is done without executable proof. Report skipped, failed, or waived checks plainly.

Common commands (`.harness/bin/just-harness-cli <cmd>`; `help <cmd>` for details):

```text
query status                              session start screen
story add --title T --lane L --verify C   start a tracked work packet
story verify --id US-xxxx                 run the story's proof
story update --id US-xxxx --status S      move a story (gate on implemented)
decision add --title T                    record a choice future work inherits
guardrail add --rule R --why W            record a standing rule
check                                     validate all records
```

No binary (fresh clone)? Read state with `cat .harness/*/*.json`; reinstall via the README one-liner.
<!-- HARNESS:END -->
