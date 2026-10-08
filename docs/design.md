# agv design

`agv` (agent-vault) lets coding agents use local secrets without holding their values.
It reduces accidental exposure (secrets in prompts, context, history, command output, files an
agent greps). It does not stop a user or agent that deliberately reads `~/.agent-vault/`, `/proc`,
or probes redaction; that is out of scope by design.

All user-facing text (prompts, errors, help, skill) is English.

## Guarantees

1. **Hard:** agv itself never writes a secret value to its own argv, stdout, stderr, logs or error
   messages. Testable; any violation is a bug.
2. **Best-effort:** output of the child process is redacted for the encodings listed under
   Redaction. Values the child transforms in other ways pass through.
3. **Best-effort:** temp files are removed (see Temp files).

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

- `agv skills` (alias `skill`) prints the embedded SKILL.md.
- `agv list [filter] [--json]`: entry names, descriptions, field names, env names and file flags,
  in stable name order, control characters stripped.
- An unknown name fails with the closest existing names. A missing secret is reported as
  "ask the user to run `agv set NAME` in a separate terminal".
- No harness gives agv a TTY or useful stdin. Commands agents use never prompt.

## Data model

An entry has a name (`^[A-Z][A-Z0-9_]*$`), a required description, a type (preset name or
`custom`) and one or more fields. A field has a name (`^[a-z][a-z0-9_]*$`), a value (bytes, may be
multi-line, never trimmed), an optional env var name, and a `file` flag. Presets only fill these
in at `set` time; changing a preset later does not change stored entries.
A single-field entry uses field `value`; `{{NAME}}` means `{{NAME.value}}`.

Name, description, type, field names, env names and file flags are plaintext metadata visible to
agents. `set` and help say so, so nobody puts a secret in a description.

## Storage

```text
~/.agent-vault/          0700   (override with AGV_HOME)
  vault.json             0600   entries; field values encrypted
  master.key             0600   32 random bytes
  .lock                         flock target for writers
```

```json
{
  "version": 1,
  "key_id": "<first 8 bytes of sha256(master.key), hex>",
  "entries": {
    "AWS_PROD": {
      "description": "AWS prod, read-only S3",
      "type": "aws",
      "created_at": "2026-10-08T10:00:00Z",
      "updated_at": "2026-10-08T10:00:00Z",
      "fields": {
        "secret_access_key": { "env": "AWS_SECRET_ACCESS_KEY", "file": false, "value": "v1:<base64(nonce|ciphertext)>" }
      }
    }
  }
}
```

- Each value is AES-256-GCM with a fresh 12-byte nonce; additional data is `NAME.field`, so a value
  moved to another field fails to decrypt.
- Writers (`set`, `rm`) hold an exclusive flock on `.lock` for the whole read-modify-write, taken
  after all prompts are answered. Readers take no lock.
- A write creates a 0600 temp file in the same directory, fsyncs it, renames it over `vault.json`,
  then fsyncs the directory.
- The key sits next to the data, so this defeats accidental reads only. A later version may move
  the key into the OS keychain without changing `vault.json`.

Key and vault states, checked on every command:

| master.key | vault.json | Result |
|---|---|---|
| missing | missing | new vault: first writer creates the key with O_EXCL under the lock, then the vault |
| present | missing | first writer creates the vault with the key's `key_id` |
| missing | present | error: key lost; entries cannot be decrypted; restore the key |
| present | present, `key_id` differs | error: wrong key for this vault |
| any | unreadable or invalid JSON | error naming the file; nothing is overwritten |

Humans are told to back up `master.key` and `vault.json` together; a lost key cannot be recovered.

## Injection (`agv run`)

```text
agv run [--env VAR=TEMPLATE]... [--env-from NAME]... [--timeout DURATION] [--allow-shell] -- CMD ARGS...
```

agv receives argv already split by the caller's shell; it never parses a command string and never
runs a shell itself. Pipes stay in the caller's shell and see redacted output.

Placeholders, in args after `CMD` and in `--env` templates:

- `{{NAME}}`, `{{NAME.field}}`: the raw value.
- `{{file:NAME}}`, `{{file:NAME.field}}`: path to a temp file holding the value.
- Grammar: exactly `{{(file:)?[A-Z][A-Z0-9_]*(\.[a-z][a-z0-9_]*)?}}`. Anything else, such as
  docker's `{{.ID}}`, passes through unchanged.
- One substitution pass; values are never re-expanded. No placeholders in `CMD` (argv[0]).
- A value containing a NUL byte cannot go into an arg or env var: error naming `NAME.field` only.

Env:

- `--env VAR=TEMPLATE` sets `VAR` to the template after substitution, for example
  `--env PGPASSWORD='{{DB.password}}'` or `--env GIT_SSH_COMMAND='ssh -i {{file:DEPLOY_KEY}}'`.
- `--env-from NAME` sets every field of NAME that has an env name; a field with `file: true` gets
  its temp-file path, others their value.
- Injected vars override inherited ones. Two flags setting the same var is an error. Other inherited
  vars, including other cloud credentials, are left alone.

The skill steers agents to env and file modes, because argument values are visible in the process
list while the child runs, and teaches single-quoting placeholders.

`CMD` whose basename is a known shell (`sh bash zsh dash fish ksh mksh tcsh csh`) is rejected unless
`--allow-shell`, since a shell can route secrets around redaction. This is a speed bump, not a
boundary; it is not extended to wrappers (`env`, `xargs`) or interpreters.

All references are resolved and all temp files written before the child starts. On any failure,
files already written are removed and nothing runs.

## Temp files

- Root: an agv-owned 0700 directory outside the workspace: `$XDG_RUNTIME_DIR/agv` if set, else
  `$TMPDIR/agv-<uid>` (or the OS temp dir).
- Each run gets its own 0700 directory named after agv's pid; files in it are 0600.
- Removed when the child exits, on timeout and on SIGINT/SIGTERM/SIGHUP. SIGKILL of agv (some
  harnesses kill the whole process group) cannot be caught, so on every start agv removes run
  directories whose pid is no longer alive.
- If the root cannot be created or written, `run` fails with a clear error before starting.
- Files disappear when the direct child exits. Commands that detach and read the file later
  (`docker run -d -v`, `ssh -f`) are not supported.

## Run lifecycle

- The child stays in agv's process group, so a harness that kills the group also kills the child.
  agv never signals its own process group.
- SIGTERM and SIGHUP received by agv are forwarded to the child. SIGINT is not forwarded: a
  terminal Ctrl-C already reaches the whole foreground group.
- `--timeout` is opt-in, no default. On expiry: SIGTERM to the child, ~2 s grace, SIGKILL, then a
  bounded wait for output. Grandchildren are not guaranteed to be killed.
- `cmd.WaitDelay` is set so a grandchild holding the pipes cannot hang agv.
- If the reader of agv's stdout goes away (`| head`), agv stops writing, waits for the child and
  cleans up instead of dying on SIGPIPE.
- Exit codes: the child's code; otherwise 124 timeout, 125 agv error, 126 not executable,
  127 not found, 128+N child killed by signal N. Every error written by agv starts with `agv: `, so
  agv's own failures can be told apart from a child that returns the same code.
- Error messages are built only from argv before substitution and `NAME.field` labels; wrapped OS
  and child errors pass through the redactor.

## Redaction

stdout and stderr of the child pass through the redactor before agv writes them, each stream with
its own state, on raw bytes. Patterns per value used in the run:

- raw;
- base64 (standard and URL-safe, with and without padding);
- URL percent-encoded;
- JSON string-escaped;
- for multi-line values, each line of 8 or more bytes.

Matches become `[REDACTED:NAME.field]`. The redactor holds back only the output tail that could
still be the start of a pattern, so prompts and progress lines are not delayed. Values used in a
run are capped (1 MiB each) to bound pattern size.

Not covered: values inside a longer encoded string (for example base64 of `user:pass` in a
`curl -v` Basic header) unless a test-vector-backed rule is added for it, other encodings, files
and network traffic of the child.

## Adding secrets (`agv set`)

- Requires a TTY when it needs to prompt; without one it fails at once with the non-interactive
  form in the message.
- Interactive: name, type (preset list), description (with the metadata warning), then each field
  with hidden input. File fields ask for a path and read it. Multi-line values that are not files
  are read from a path too. A summary shows field names, byte lengths, env names and file flags,
  never values, before confirming. On save it prints how an agent would use the entry.
- Existing entry: fields are patched. An empty answer keeps the current value; a field is removed
  only with `--unset field`. Changing the type keeps fields with matching names and asks for the
  rest.
- Warnings: value shorter than 6 bytes (redaction false positives), trailing whitespace or newline
  (kept as typed, never trimmed).
- Non-interactive: `agv set NAME --type T --desc D --field f=@path --field g=-` (`-` = stdin, at
  most one field). Values are never accepted inline in argv.
- `agv rm NAME` asks y/N unless `--yes`.

Presets (v1), env names filled in at `set`:

| Preset | Fields (env) |
|---|---|
| `api-token` | `value` |
| `basic` | `username`, `password` |
| `db-url` | `url` (`DATABASE_URL`) |
| `aws` | `access_key_id` (`AWS_ACCESS_KEY_ID`), `secret_access_key` (`AWS_SECRET_ACCESS_KEY`), `session_token` (`AWS_SESSION_TOKEN`, optional), `region` (`AWS_REGION`) |
| `azure-sp` | `tenant_id` (`AZURE_TENANT_ID`), `client_id` (`AZURE_CLIENT_ID`), `client_secret` (`AZURE_CLIENT_SECRET`) |
| `gcp-sa` | `key` file (`GOOGLE_APPLICATION_CREDENTIALS`) |
| `ssh-key` | `key` file |
| `custom` | user-defined |

## Code layout

```text
cmd/agv/main.go     os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
internal/
  cli/     command dispatch, prompts, all user-facing text
  vault/   entries, validation, encryption, key/vault states, locking, atomic save
  preset/  built-in preset table
  inject/  placeholders and env -> Plan{Argv, Env, Patterns, Cleanup}
  runner/  start the child, signals, timeout, pipe output through redact, exit codes, temp cleanup
  redact/  streaming multi-pattern redactor
  skill/   embedded SKILL.md (go:embed)
```

Dependencies point down only: `cli` → `inject`, `runner`, `preset`, `vault`, `skill`;
`runner` → `inject` (Plan type), `redact`. `inject` does not import `vault`: it reads entries through
its `Source` interface, which `cli` implements on the vault. `inject` resolves everything into one
`Plan`; `runner`
alone owns signals and cleanup. `AGV_HOME` lets tests use a temp directory, never the real one.
Standard library plus `golang.org/x/term`. Unix-only calls (flock, signals, process checks) sit in
`_unix.go` files; nothing is written for Windows yet.

## Not in v1

Windows, OS keychain, user-defined presets, a separate `edit` command, testing a credential,
non-secret (visible) fields, audit log, hex encodings, detecting shell wrappers or interpreters,
supervising detached processes.
