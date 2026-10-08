# US-ewhv vault, presets and set/list/rm commands

<!-- Status, lane, verify command, and proof live in .harness/stories/US-ewhv.json.
     Read them with `just-harness-cli query stories`. Do not copy them here. -->

## Goal

Humans can store, update, list and remove secrets with `agv set|list|rm`; agents can list them.
No command ever prints a value. `internal/vault` also gives `cli` what `agv run` needs: entry
metadata, the decrypted fields of one entry, and an unknown-name error.

## Acceptance

- Storage follows docs/design.md (Storage): AES-256-GCM per field with AAD `NAME.field`, `key_id`,
  every row of the key/vault state table, exclusive flock for `set`/`rm` taken after the prompts,
  atomic 0600 save, `AGV_HOME` else `$HOME/.agent-vault`.
- `set`: hidden prompts or `--field F=@path|-`; never an inline value; a changed type keeps fields
  with matching names; a field is removed only by `--unset`; values are kept exactly as given,
  with warnings for short or whitespace-padded values.
- `list [filter] [--json]`: names, descriptions, types, fields, env names, file flags, in name
  order, control characters stripped. `rm NAME`: y/N unless `--yes`.
- Nothing agv prints or logs contains a value, including errors about mistyped arguments (G-7n9t).

## Scope

- In: `internal/vault`, `internal/preset`, `internal/cli/{set,list,rm}*.go`, `go.mod`/`go.sum`.
- Out: `cli.go`, `run`, `skills`, `inject`, `runner`, `redact`; the adapter from vault to
  `inject.Source`.

## Proof

`go vet ./... && go test -race ./internal/vault/... ./internal/preset/... ./internal/cli/...`
covers: encrypt/decrypt round trip with byte-exact values, a value moved to another field or
entry fails, each state-table row (and that failures change nothing), concurrent writers lose
nothing, file modes, patch semantics and validation, no value in any `set`/`list`/`rm` output
or error, hidden input for values, locking only after the last prompt, control-character
stripping. Manual: a real pty run of the prompts, including Ctrl-C at a hidden prompt (echo is
restored) and Ctrl-D (nothing written).

## Handoff

Done and verified on branch `dev-vault`. Open for the integrator:

- Adapter: `vault.Fields(name)` returns `map[string]vault.Field`, laid out like `inject.Field`.
  `*vault.NotFoundError` carries `Name` and `Suggestions`; its text has no "ask the user to run
  `agv set NAME`" advice, so the adapter or `run` adds it.
- `vault.MaxValueSize` (1 MiB) is enforced by `set`; `redact`/`inject` may reuse it.
- Custom entries made with `--field` carry no env name or file flag; those are asked only in the
  interactive walk.
