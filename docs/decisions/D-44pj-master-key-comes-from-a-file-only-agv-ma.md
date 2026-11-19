# D-44pj Master key comes from a file only: AGV_MASTER_KEY_FILE, never from an environment value

<!-- Status lives in .harness/decisions/D-44pj.json. Change it with `just-harness-cli decision update`. -->

## Context

`master.key` sits next to `vault.json`, so a copy or backup of the vault directory carries the key
that decrypts it. Issue #2 asked for the key to come from elsewhere through two variables:
`AGV_MASTER_KEY` (the key itself) and `AGV_MASTER_KEY_FILE` (a path to it), with `agv run` removing
the first from the child's environment and masking it in output. The case it named is a container
whose key is a Docker secret, which is a file (`/run/secrets/...`).

A key held in an environment variable is inherited by every process that shell starts, not only
by `agv run`, and shows up in `env`, CI logs and crash reports. An agent at that shell could print
it with `echo`. Reading `master.key` also works for an agent on the same account, but it has to go
looking for the file.

## Decision

Accepted by the user (2026-11-19), on storm's proposal:

- `AGV_MASTER_KEY_FILE` names a file holding the key, in the same format as `master.key`. There is
  no `AGV_MASTER_KEY`.
- With the variable set, agv reads the key only from that file and never creates a key. A missing or
  malformed file, an empty variable, or a `master.key` left in the vault directory is an error.
- The key is checked on every command, even while the vault is still empty, so `agv list` works as
  a health check.
- Errors name the variable, never its value: a key pasted into it by mistake must not reach output.
- `agv run` passes the variable to the child unchanged (it is a path), so `agv` inside `agv run`
  keeps working. There is no extra masking.
- The existing `key_id` check gives the wrong-key error, as for `master.key`.

## Consequences

- A backup of the vault directory alone no longer carries the key. On the same machine, an agent can
  still read the key file. This separates key from data at rest; it does not hide the key from local
  processes.
- Without the variable, nothing changes from v0.2.0.
- Do not add a variable that holds the key itself unless the user reopens this. It can be added
  later without breaking anyone; removing it later would break people.
- Do not make agv create the key file: a mount that failed would otherwise yield a fresh key in a
  throwaway place, and every secret written under it is lost when that place goes.
