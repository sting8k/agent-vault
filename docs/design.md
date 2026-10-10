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

Without a terminal (how agents run commands), `set` may only add a new entry. Changing or removing
an existing entry needs the person at a terminal and a confirmation, because the vault holds the
only copy of a value. Error messages never suggest a way around this.

## Agent flow

```text
AGENTS.md line -> agv skills -> agv list [filter] -> agv run ...
```

- `agv skills` (alias `skill`) prints the embedded SKILL.md.
- `agv list [filter] [--json]`: entry names, descriptions, field names, env names and file flags,
  in stable name order, control characters stripped.
- An unknown name fails with the closest existing names. A missing secret is reported as
  "ask the user to run `agv set NAME` in a separate terminal".
- Harnesses run agent commands in their own session with no TTY (pi, Claude Code, and Codex
  unless a call asks for `tty=true`). Commands agents use never prompt. Exception: Codex
  `exec_command` with `tty=true` gives the agent a PTY it can type into, so there it can answer
  `set`/`rm` confirmations itself; the terminal rule only stops agents that have no PTY.

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
  master.key             0600   32 random bytes as 64 hex characters + newline
  .lock                         flock target for writers
  config.json            0600   audit log and webhook settings; written by hand, optional
  audit.log              0600   one JSON line per run, set and rm
  audit.lock                    flock target for audit.log writers
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
The key is text so it can be pasted into a password manager and back; surrounding whitespace is
ignored on read, anything else malformed is an error (never a new key).

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
  its temp-file path, others their value. If NAME has no field with an env name it is an error that
  points to `--env VAR='{{NAME.field}}'`.
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
- The child inherits agv's stdin (`agv run -- psql ... < file.sql` works).
- `--timeout` is opt-in, no default; it takes a Go duration (`30s`, `2m`). On expiry: SIGTERM to the child, ~2 s grace, SIGKILL, then a
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

- Prompts and confirmations use the terminal: stdin when it is one, else the controlling terminal
  (`/dev/tty`, as git and sudo do) when stderr is a terminal too, so stdin can carry a value.
  Harnesses run commands in a session of their own, where `/dev/tty` does not open. When it needs
  to prompt and there is no terminal, `set` fails at once with the non-interactive form in the
  message.
- Interactive: name, type (preset list), description (with the metadata warning), then each field
  with hidden input. File fields ask for a path and read it. Multi-line values that are not files
  are read from a path too. A summary shows field names, byte lengths, env names and file flags,
  never values, before confirming. On save it prints how an agent would use the entry.
- Existing entry: fields are patched. An empty answer keeps the current value; a field is removed
  only with `--unset field`. Changing the type keeps fields with matching names and asks for the
  rest.
- Warnings: value shorter than 6 bytes (redaction false positives), trailing whitespace or newline
  left in the value.
- Non-interactive: `agv set NAME --type T --desc D --field f=@path --field g=-` (`-` = stdin, at
  most one field). From stdin exactly one trailing `\n` or `\r\n` is dropped, as
  `docker login --password-stdin` does, so `gh auth token | agv set ...` stores the token. `@path`
  and hidden prompts are byte-exact: a PEM file legitimately ends in a newline. Values are never
  accepted inline in argv. Without a terminal this adds new entries only; with one, an existing
  entry is still confirmed there.
- `agv rm NAME` always asks y/N at a terminal; there is no `--yes`.

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

## Audit log and webhooks

Settings live in `$AGV_HOME/config.json`, edited by hand: there is no command for it and no
environment variable overrides it. A missing file means the defaults. A file that cannot be read,
parsed or validated (unknown keys, an unknown `format` or action, a bad duration, a literal URL
that is not http(s)) is an error that names the file; `run`, `set` and `rm` then fail with 125
before doing anything. `list` and `skills` do not read it.

```json
{
  "audit": { "enabled": true, "retention": "7d" },
  "webhooks": [
    { "url": "{{NTFY_TOPIC}}", "format": "ntfy", "actions": ["run", "rm"], "secrets": ["AWS_PROD"] }
  ]
}
```

- `audit.enabled` (default `true`) turns the log off; webhooks do not depend on it.
  `audit.retention` is `Nd` (whole days) or a Go duration such as `36h`; default `7d`.
- A webhook has `url` (required), `format` (`json`, the default, or `ntfy`), `actions` (any of
  `run`, `set`, `rm`) and `secrets` (`NAME` or `NAME.field`). An empty or missing filter matches
  everything; both filters must match. `NAME` matches every field of NAME, `NAME.field` only that one.

### Audit log

`audit.log` is one file, 0600, never rotated into copies. A record is one JSON line:

```json
{"time":"2026-10-10T13:45:37.554Z","action":"run","secrets":["AWS_PROD.region"],"program":"aws","cwd":"/home/me/proj","exit":0,"duration_ms":1234}
```

`secrets` holds `NAME.field` labels for `run` and `NAME` for `set` and `rm`. `program` is argv[0]
only (`run` only). A record has no field for a value, an argument or the environment, so none can
reach the log or a webhook body. `time` is when the action finished.

- Logged: `run`, `set`, `rm`; not `list` or `skills`. A `run` is logged once its references are
  resolved, whatever happens next: exit codes 124 to 127 and 128+N are recorded like any other,
  and so is a refused shell (125). A run that fails earlier (bad flags, unknown secret) read
  nothing and is not logged. `set` and `rm` are logged when the vault was changed; a refused or
  cancelled one changed nothing and is not.
- When: `run` writes its record from `runner.Options.Finished`, after the child has exited and the
  temp files are removed but before agv releases its signal handlers. A timeout, or a SIGTERM,
  SIGHUP or SIGINT that ends the child, is therefore logged; SIGKILL of agv cannot be.
- Concurrency: writers take an exclusive flock on `audit.lock` (not the vault's `.lock`, so a big
  rewrite never makes `agv set` wait). Under it a writer prunes and then appends.
- Pruning: lines are appended in time order, so a writer reads only the first line. Once it is
  older than the retention, the writer rewrites the file without every expired line, through a
  temp file and a rename, so a reader sees the old file or the new one. A line with no readable
  time is kept. The rewrite costs time proportional to the file (about 13 ms for 7,000 lines, 1.4
  MB); between rewrites an append is O(1). After a rewrite `tail -f` keeps following the replaced
  file; use `tail -F`. A torn last line (crash or full disk in the middle of a write) is not repaired.
- If the log cannot be written agv prints `agv: audit log: ...` on stderr and the command carries
  on with its own exit code.

### Webhooks

A finished action that matches a webhook is sent once, at the end. All matching webhooks go out at
the same time and share one 3 second limit. A failure is a warning, `agv: webhook #N: ...` (N counts
the config list from 1), and never changes the exit code or fails the command. The warning is a
fixed reason (timed out, could not connect, HTTP status, unknown secret, bad URL): it never
contains the URL, a transport error or text from the server, because the URL may be a secret.
Redirects are not followed.

- `url` is a template: `{{NAME}}` and `{{NAME.field}}` are filled in from the vault when the
  webhook is sent, anywhere in the string (`https://ntfy.sh/{{NTFY_TOPIC}}`); `{{file:...}}` is an
  error. Keep ntfy topics and Slack or Discord URLs in the vault: a URL written literally sits in
  config.json in plain text. A missing secret or field is reported like any webhook failure,
  with the name only.
- `json` posts the audit record as `application/json`. Slack (needs `text`) and Discord (needs
  `content`) answer HTTP 400 to it; use `json` for receivers that take arbitrary JSON.
- `ntfy` posts a short plain-text body (secret names, exit code, duration, directory) with `Title`,
  `Priority` (`default`; `high` when the exit code is not 0) and `Tags` headers. Headers are
  printable ASCII.
- For `run`, webhooks are sent after `runner.Run` has returned, so Ctrl-C can cut them short; the
  audit line is already written by then.

## Code layout

```text
cmd/agv/main.go     os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
internal/
  cli/     command dispatch, prompts, all user-facing text
  vault/   entries, validation, encryption, key/vault states, locking, atomic save
  preset/  built-in preset table
  inject/  placeholders and env -> Plan{Argv, Env, Patterns, Cleanup}; Expand for text agv itself uses
  runner/  start the child, signals, timeout, pipe output through redact, exit codes, temp cleanup
  audit/   config.json, audit.log (append, prune, flock), webhook delivery
  redact/  streaming multi-pattern redactor
  skill/   embedded SKILL.md (go:embed)
```

Dependencies point down only: `cli` → `inject`, `runner`, `preset`, `vault`, `audit`, `skill`;
`runner` → `inject` (Plan type), `redact`. `inject` does not import `vault`: it reads entries through
its `Source` interface, which `cli` implements on the vault. `audit` imports no other agv package:
`cli` hands it the directory and a function that fills in a webhook URL template. `inject` resolves everything into one
`Plan`; `runner`
alone owns signals and cleanup. `AGV_HOME` lets tests use a temp directory, never the real one.
Standard library plus `golang.org/x/term`. Unix-only calls (flock, signals, process checks) sit in
`_unix.go` files; nothing is written for Windows yet.

## Not in v1

Windows, OS keychain, user-defined presets, a separate `edit` command, testing a credential,
non-secret (visible) fields, hex encodings, detecting shell wrappers or interpreters,
supervising detached processes.
