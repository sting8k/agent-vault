# D-zb05 Store in ~/.agent-vault as JSON with AES-GCM field values and a local master key

<!-- Status lives in .harness/decisions/D-zb05.json. Change it with `just-harness-cli decision update`. -->

## Context

Secrets live locally per user. A plain file is dumped into context the first time an agent greps or cats it.

## Decision

`~/.agent-vault/vault.json` (0600) with per-field AES-256-GCM values, and `master.key` (0600) next to it. Format in docs/design.md.

## Consequences

Defeats accidental reads only, by design. `version` field allows migration; the key can move to an OS keychain later without changing vault.json.
