# Agent Instructions

Add project-specific instructions above or below the harness block.

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
