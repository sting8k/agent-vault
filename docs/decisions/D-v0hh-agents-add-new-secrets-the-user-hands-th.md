# D-v0hh Agents add new secrets the user hands them; changing and removing stay with the user at a terminal; no command prints a value

<!-- Status lives in .harness/decisions/D-v0hh.json. Change it with `just-harness-cli decision update`. -->

## Context

D-vehd said "Humans use `set` and `rm`; agents use `list`, `run`, `skills`", and the agent skill
told agents not to run `set`. In practice the user often gives the agent a credential in chat and
has it stored, the first time (user, 2026-10-27: "trên thực tế đôi khi toàn đưa cred cho agent set
hộ là nhiều (nhưng là set lần đầu, sure)"). The code already allowed this: without a terminal,
`set` only adds new entries. D-vehd's consequences already noted that such an add leaks nothing new.

## Decision

Supersedes D-vehd and keeps the rest of it.

- An agent may add a **new** entry whose value the user gave it, passing the value on stdin
  (`--field f=-`) or from a file (`--field f=@path`), never in argv, a name or a description.
- Changing or removing a stored entry needs the person at a terminal and a confirmation (`rm` has
  no `--yes`). Error messages never suggest a way around this.
- No command prints a value. `set` takes values from a hidden prompt, a file or stdin.

## Consequences

- The skill and agv's messages tell an agent to add a secret it was given, and to ask the user to
  run `agv set` only when it was not given the value. They never tell it to ask for a value.
- An agent cannot overwrite or delete the only copy of a stored value by accident. A deliberate
  agent can fake a terminal (`script`); out of scope.
