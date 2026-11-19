# US-k9tj Master key from AGV_MASTER_KEY_FILE (issue #2)

<!-- Status, lane, verify command, and proof live in .harness/stories/US-k9tj.json.
     Read them with `just-harness-cli query stories`. Do not copy them here. -->

## Goal

Let the master key live outside the vault directory, so a copy or backup of the vault does not carry
the key (GitHub issue #2). The rules are in D-44pj.

## Acceptance

- With `AGV_MASTER_KEY_FILE` set, every command reads the key from that file; `master.key` is
  neither read nor created.
- A vault made with `master.key` opens after the key file is moved out and the variable points at
  it; a vault made with the variable opens again later with the same file.
- Missing or malformed key file, empty variable, `master.key` still in the vault directory, or a key
  that does not match the vault's `key_id`: an error, and no key or vault file is written.
- No error contains the variable's value (tested with a key pasted in place of a path).
- Without the variable, behaviour is unchanged (the existing state-table tests pass unchanged).

## Scope

- In: `internal/vault` (key source, checks), the first-`set` backup line, `agv set --help`,
  README, docs/design.md.
- Out: a variable that holds the key itself, masking the key in `agv run`, removing the variable
  from the child's environment, OS keychains.

## Proof

The verify command runs gofmt, vet and `go test -race ./...`; the tests in
`internal/vault/vault_test.go` (`TestKeyFile...`) cover the acceptance list. Manual: a built binary
with a temp `AGV_HOME`, moving a real-format key out and back.

## Handoff

Implemented in one session on `dev-local`. Release (v0.3.0) needs the user's approval.
