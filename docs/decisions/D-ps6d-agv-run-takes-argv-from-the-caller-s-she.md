# D-ps6d agv run takes argv from the caller's shell, runs no shell, injects via arg, env or temp file

<!-- Status lives in .harness/decisions/D-ps6d.json. Change it with `just-harness-cli decision update`. -->

## Context

secretsh parses a command string with its own tokenizer, which blocks pipes and is its largest attack surface. Tools read secrets from args, env vars or files.

## Decision

`agv run -- CMD ARGS...` takes argv already split by the caller's shell; agv never parses a command string or runs a shell (shells as argv[0] rejected unless `--allow-shell`). Injection by `{{NAME.field}}` in args, `--env`/`--env-from`, or `{{file:NAME}}` temp files. Output is stream-redacted. See docs/design.md.

## Consequences

No tokenizer to build. Pipes work in the caller's shell on redacted output. Values in args are visible in the process list while the child runs, so the skill steers agents to env and file modes.
