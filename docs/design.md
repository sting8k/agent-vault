# agv design

`agv` (agent-vault) lets coding agents use local secrets without holding their values.
It reduces accidental exposure (secrets in prompts, context, history, output). It does not stop a
user or agent that deliberately reads `~/.agent-vault/`; that is out of scope by design.

All user-facing text (prompts, errors, help, skill) is English.

## Roles

| Actor | Commands | Sees values? |
|---|---|---|
| Human | `set`, `rm`, plus everything below | only what they type |
| Agent | `list`, `run`, `skills` | never |

There is no command that prints a secret value.

## Agent flow

```text
AGENTS.md line -> agv skills -> agv list [filter] -> agv run ...
```

- `agv skills` (alias `skill`) prints the embedded SKILL.md: syntax, rules, examples.
- `agv list [filter] [--json]` prints entry names, descriptions, field names and env mappings.
- An unknown name fails with the closest existing names. A missing secret is reported to the human
  ("ask the user to run `agv set NAME`"), never requested as a value.

## Data model

An entry has a name (`^[A-Z][A-Z0-9_]*$`), a required description, a type (preset name or
`custom`) and one or more fields. A field has a name (`^[a-z][a-z0-9_]*$`), a value (bytes, may be
multi-line) and an optional env var name. A single-field entry uses field `value`;
`{{NAME}}` is shorthand for `{{NAME.value}}`.

## Storage

```text
~/.agent-vault/          0700
  vault.json             0600   entries; field values encrypted
  master.key             0600   32 random bytes, created on first write
```

```json
{
  "version": 1,
  "entries": {
    "AWS_PROD": {
      "description": "AWS prod, read-only S3",
      "type": "aws",
      "created_at": "2026-10-08T10:00:00Z",
      "updated_at": "2026-10-08T10:00:00Z",
      "fields": {
        "secret_access_key": { "env": "AWS_SECRET_ACCESS_KEY", "value": "v1:<base64(nonce|ciphertext)>" }
      }
    }
  }
}
```

- Each value is AES-256-GCM with a fresh 12-byte nonce; the entry and field name are the
  additional data, so a value moved to another field fails to decrypt.
- Writes are atomic (temp file in the same directory, fsync, rename).
- The key sits next to the data: this defeats accidental reads only. A later version may move
  `master.key` into the OS keychain without changing `vault.json`.

## Injection (`agv run`)

```text
agv run [--env VAR=NAME.field]... [--env-from NAME]... [--timeout D] [--allow-shell] -- CMD ARGS...
```

| Mode | Syntax | Use for |
|---|---|---|
| Argument | `{{NAME}}` / `{{NAME.field}}` inside any arg | tokens in headers, flags, URLs |
| Env var | `--env VAR=NAME.field`, `--env-from NAME` (all mapped fields) | AWS, Azure, `PGPASSWORD`, … |
| Temp file | `{{file:NAME}}` / `{{file:NAME.field}}` → path | SSH keys, kubeconfig, service-account JSON |

- agv receives argv after `--` from the caller's shell; it never parses a command string and
  never runs a shell itself. Pipes stay in the caller's shell and see redacted output:
  `agv run -- curl ... | jq .`.
- Prefer env and file modes: argument values are visible in the process list while the child runs.
- `argv[0]` that is a known shell (`sh bash zsh dash fish ksh mksh tcsh csh`, by basename) is
  rejected unless `--allow-shell`, because a shell can route secrets around redaction.
- Temp files live in a 0700 directory, are 0600, and are removed when the child exits or agv is
  interrupted.
- Exit codes: child's code; 124 timeout; 125 agv error; 126 not executable; 127 not found;
  128+N killed by signal N.

## Redaction

stdout and stderr of the child are streamed through a redactor before agv writes them.
Each used secret is matched raw and as base64 (std, url), URL-encoded and hex (lower, upper);
multi-line values are also matched per line (lines of 8+ bytes). Matches become `[REDACTED:NAME.field]`.
The redactor holds back `maxPatternLen-1` bytes between chunks, so output streams without
missing matches that cross a chunk boundary. Files and network traffic of the child are not covered.

## Adding secrets (`agv set`)

- Interactive on a TTY: name, type (preset list), description, then each field with hidden input;
  file-type fields ask for a path. A summary shows field names, byte lengths and env mappings
  (never values) before confirming. On save it prints how an agent would use the entry.
- Existing entry: an empty answer keeps the field's current value.
- Warnings: value shorter than 6 bytes (redaction false positives), trailing whitespace/newline.
- Non-interactive: `agv set NAME --type T --desc D --field f=@path --field g=-` (`-` = stdin).
  Values are never accepted inline in argv.
- `agv rm NAME` asks y/N unless `--yes`.

Presets (v1): `api-token`, `basic`, `db-url`, `aws`, `azure-sp`, `gcp-sa` (file), `ssh-key` (file),
`custom`.

## Code layout

```text
cmd/agv/main.go            calls cli.Main(os.Args) and exits with its code
internal/
  vault/    entries, validation, encryption, atomic load/save of ~/.agent-vault
  preset/   built-in preset table (fields, env names, file flag)
  inject/   resolve {{…}} and --env/--env-from into argv, env and temp files
  redact/   streaming multi-pattern redactor with encoded variants
  runner/   start the child (os/exec), pipe output through redact, timeout, signals, exit codes
  cli/      command dispatch, prompts, output text; owns all user-facing strings
  skill/    embedded SKILL.md (go:embed)
```

Dependencies point down only: `cli` → `inject`, `runner`, `preset`, `vault`, `skill`;
`runner` → `redact`; `inject` → `vault`. `vault`, `redact`, `preset` and `skill` import no
internal package. Standard library plus `golang.org/x/term` (hidden input) only.

Platform code (shell basenames, signals, file modes) sits behind build tags in the package that
needs it, so Windows can be added later without touching the rest.

## Not in v1

Windows, OS keychain, user-defined presets, a separate `edit` command, testing a credential,
non-secret (visible) fields, audit log.
