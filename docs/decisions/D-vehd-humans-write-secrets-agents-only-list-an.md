# D-vehd Humans write secrets, agents only list and run; no command prints a value

<!-- Status lives in .harness/decisions/D-vehd.json. Change it with `just-harness-cli decision update`. -->

## Context

If the tool can print a value, an agent will eventually call it and the value lands in its context.

## Decision

Humans use `set` and `rm`; agents use `list`, `run`, `skills`. No command prints a value. `set` takes values from a hidden prompt, a file (`@path`) or stdin (`-`), never inline argv. Without a terminal, `set` only adds new entries; changing or removing an entry needs a terminal and a confirmation (`rm` has no `--yes`). See docs/design.md.

## Consequences

A human who needs to read a value opens it some other way. An agent can still add an entry whose value it already holds, which leaks nothing new. It cannot destroy the only copy of a stored value by accident. A deliberate agent can fake a terminal (`script`); out of scope. Scripts that update entries need a terminal for now.
