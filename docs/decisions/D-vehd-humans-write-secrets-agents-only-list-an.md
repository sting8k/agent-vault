# D-vehd Humans write secrets, agents only list and run; no command prints a value

<!-- Status lives in .harness/decisions/D-vehd.json. Change it with `just-harness-cli decision update`. -->

## Context

If the tool can print a value, an agent will eventually call it and the value lands in its context.

## Decision

Humans use `set` and `rm`; agents use `list`, `run`, `skills`. No command prints a value. `set` takes values from a hidden prompt, a file (`@path`) or stdin (`-`), never inline argv. See docs/design.md.

## Consequences

A human who needs to read a value opens it some other way. A deliberate agent can still pipe values into `set`; out of scope.
