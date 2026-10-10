# agv: agent-vault

Let your coding agent use your API keys, passwords and SSH keys **without ever seeing them**.

```text
you:    agv set GITHUB                      (typed once, hidden, stored encrypted)
agent:  agv run --env GH_TOKEN='{{GITHUB}}' -- gh api user
                 └─ agv injects the real token, runs gh, and masks the token in the output
```

The agent only ever handles the name `GITHUB`. The value never goes into its prompt, its context,
your shell history or (in the usual encodings) the command output it reads back.

**What it is not:** a hardened vault. Secrets are stored on your own disk, and the decryption key
sits next to them. agv stops *accidental* leaks. It does not stop someone, or an agent, who
deliberately goes looking. See [Limits](#limits).

## Install

macOS and Linux (amd64, arm64):

```sh
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
URL="https://github.com/sting8k/agent-vault/releases/latest/download/agv-$OS-$ARCH"
curl -fsSLO "$URL" && curl -fsSL "$URL.sha256" | shasum -a 256 -c \
  && chmod +x "agv-$OS-$ARCH" && sudo mv "agv-$OS-$ARCH" /usr/local/bin/agv
```

Nothing is installed if the checksum does not match. Without sudo, move it to `~/.local/bin/agv`
instead (it must be on your `PATH`). Run the same lines again to upgrade; `agv --version` shows
what you have.

Or build it with Go 1.24+: `go install github.com/sting8k/agent-vault/cmd/agv@latest`.

## Add a secret

Run this in **your own terminal**, not through the agent:

```console
$ agv set
Name (A-Z, 0-9, _): AWS_PROD
Types: api-token, basic, db-url, aws, azure-sp, gcp-sa, ssh-key, custom
Type: aws
Description (agents can read it with 'agv list'; never put a secret here): AWS prod, read-only S3
Value for AWS_PROD.access_key_id (hidden; @path reads a file):
Value for AWS_PROD.secret_access_key (hidden; @path reads a file):
...
Save? [Y/n]
```

Each type fills in the right fields and environment variables for you; `custom` lets you define
your own. Running `agv set NAME` again updates an entry, and an empty answer keeps the current
value. `agv rm NAME` deletes one.

Write descriptions that help the agent choose: "GitHub PAT, read-only, org acme" is useful,
"token" is not. Names and descriptions are **not** encrypted, so never put a secret in them.

## Let your agent use it

Add one line to your agent's **global** instructions (system prompt), so every project knows
about agv, for example `~/.claude/CLAUDE.md` (Claude Code) or `~/.codex/AGENTS.md` (Codex):

```text
When a command needs a secret (API key, token, password, SSH key), prefer getting it through
the agv CLI (see "agv skills") rather than asking for it or reading it from files.
```

Optionally install the full instructions as a skill, so the agent does not need to run
`agv skills` itself:

```sh
mkdir -p ~/.claude/skills/agv && agv skills > ~/.claude/skills/agv/SKILL.md   # Claude Code
```

The agent then runs `agv list` to see what exists (names and descriptions only) and `agv run` to
use it:

```sh
agv run --env-from AWS_PROD -- aws s3 ls                          # as environment variables
agv run --env GIT_SSH_COMMAND='ssh -i {{file:DEPLOY_KEY}}' -- git pull   # as a temp file
agv run -- curl -H 'Authorization: Bearer {{GITHUB}}' https://api.github.com/user
```

## Audit log and notifications

agv appends one line per `run`, `set` and `rm` to `~/.agent-vault/audit.log`: time, secret names,
the program (never its arguments), directory, exit code and duration. It never holds a value. It
is one file; lines older than 7 days are dropped.

To change that, or to be notified when an agent uses a secret, write `~/.agent-vault/config.json`
by hand (`chmod 600`):

```json
{
  "audit": { "enabled": true, "retention": "7d" },
  "webhooks": [
    { "url": "{{NTFY_TOPIC}}", "format": "ntfy", "actions": ["run", "rm"], "secrets": ["AWS_PROD"] }
  ]
}
```

- `retention` is days (`14d`) or hours (`36h`). `"enabled": false` stops the log.
- `format` is `ntfy` (a readable message for the ntfy app) or `json` (the log line, for your own
  receiver; Slack and Discord reject it). `actions` and `secrets` narrow what is sent; leave them
  out to get everything. `"secrets": ["AWS_PROD"]` covers all fields of AWS_PROD.
- A topic or webhook URL is a secret too: store it with `agv set NTFY_TOPIC` (type `api-token`,
  value `https://ntfy.sh/<your-topic>`) and write `{{NTFY_TOPIC}}` as the `url`. agv never prints it.
- A log or webhook that fails is a warning on stderr; the command still runs and keeps its exit
  code. A `config.json` agv cannot read is an error until you fix it.

## Limits

- **Storage:** `~/.agent-vault/` holds `vault.json` (values encrypted with AES-256-GCM) and
  `master.key`. Anyone who can read both can decrypt. **Back up both files together**: a lost
  key cannot be recovered. The key is one line of hex text, so a password manager is a good place
  for it (keep it apart from the vault backup).
- **Output masking is best-effort.** agv masks the raw value and its base64, URL-encoded and
  JSON-escaped forms. A command that reverses, slices or otherwise rewrites a secret gets past it,
  and so does anything the command writes to files or sends over the network.
- **Process list:** values passed in arguments (`{{NAME}}` in an argument) are visible in `ps`
  while the command runs. Prefer `--env` / `--env-from` / `{{file:…}}`.
- **Shells are refused** as the command (`agv run -- bash -c …`), because a shell can easily route
  a secret around masking. `--allow-shell` overrides this.
- **Temp files** for `{{file:…}}` live under `$XDG_RUNTIME_DIR/agv` or `$TMPDIR` and are deleted
  when the command exits. If agv is killed with SIGKILL, the next agv run cleans them up.
- **The audit log is a record, not a lock:** anyone who can write `~/.agent-vault/` can edit it.
  A run killed with SIGKILL is not logged.
- **Platforms:** Linux and macOS. Windows is planned.

## More

- [docs/design.md](docs/design.md): full design and guarantees
- `agv help`, `agv set --help`, `agv run --help`
