# Changelog

All notable changes to agv. Each release on GitHub shows the section for its tag.

## [0.2.0] - 2026-10-27

### Added

- Groups in names: `SHOP/PROD/DB`, `SHOP/STAGING/DB`, `PERSONAL/GITHUB`. The full name is the one
  name everywhere (`{{SHOP/PROD/DB.url}}`, `--env-from`, redaction labels, the audit log) and works
  from any directory. `agv list` keeps each group together; `agv list shop` filters by name or
  description. Names without `/` work as before.
- Typo help that hands back the name to use: a misspelling, wrong case, the last part alone (`DB`
  lists every `*/DB`) or a group. It never offers another environment (`SHOP/DEV/DB` is not "fixed"
  to `SHOP/PROD/DB`); instead it shows what the group holds, marked as other secrets.
- `agv set` asks before creating a group that looks like a typo of an existing one (`SHPO/` next to
  `SHOP/`). Without a terminal it refuses, unless `--new-group` says the group really is new.
  `agv set` with no name shows the groups in use.
- Webhook `secrets` filters take groups (`SHOP/PROD/`) and `*` for one whole part (`*/PROD/`, every
  production secret).
- `agv skills` teaches agents to name secrets `PROJECT/ENV/SERVICE` with ENV `PROD`, `STAGING` or
  `DEV`, and to write descriptions that name the service, environment and account.

### Changed

- Agents may add a new secret you give them (the value on stdin or from a file, never as an
  argument). Changing or removing a stored secret still needs you at a terminal.
- A placeholder that names a stored secret in the wrong case (`{{shop/prod/db}}`) is now an error
  with the placeholder to write, instead of being passed through as text. Other tools' templates
  (`{{.ID}}`, `{{ .Values.x }}`) still pass untouched.
- Built with Go 1.27.3, which fixes a security issue in `crypto/x509`.

## [0.1.2] - 2026-10-10

### Added

- Audit log: every `agv run`, `set` and `rm` adds one line to `~/.agent-vault/audit.log` with the
  time, secret names, the program (never its arguments), directory, exit code and duration. It
  never holds a value. One file; lines older than 7 days are dropped.
- Notifications: webhooks in `~/.agent-vault/config.json` tell you when chosen actions or secrets
  are used. Formats: `ntfy` (readable in the ntfy app) and `json` (the log line, for your own
  receiver). The URL can be a stored secret (`{{NTFY_TOPIC}}`) and is never printed.
- `config.json` sets the retention, turns the log off, and lists webhooks. An unreadable or
  misspelled config stops `run`, `set` and `rm` until you fix it, so a typo cannot silently turn
  auditing off.

### Changed

- A failing audit log or webhook is a warning on stderr; the command still runs and keeps its exit
  code.
- Built with Go 1.27.2, which fixes security issues in `net/http` and `crypto/tls` that the new
  webhook sending would otherwise reach.

## [0.1.1] - 2026-10-08

First public release.

### Added

- `agv set`: add or update a secret from your terminal. Values are entered hidden, read from a file
  (`@path`) or from stdin (`-`), never from the command line. Presets fill in fields and
  environment variable names for `api-token`, `basic`, `db-url`, `aws`, `azure-sp`, `gcp-sa`,
  `ssh-key`, or define your own with `custom`.
- `agv list`: names, descriptions, fields and env names (never values), with a filter and `--json`.
- `agv run`: run a command with secrets injected as `{{NAME.field}}` arguments, environment
  variables (`--env`, `--env-from`) or temp files (`{{file:NAME}}`). The command's output is
  redacted (raw, base64, URL-encoded and JSON-escaped forms). Shells are refused as the command
  unless `--allow-shell`. Optional `--timeout`.
- `agv rm`: remove a secret after confirming at your terminal.
- `agv skills`: usage instructions for coding agents, ready to install as a skill.
- Storage in `~/.agent-vault/`: values encrypted with AES-256-GCM, key in `master.key` as one line
  of hex text.
- Releases for Linux and macOS (amd64, arm64) with SHA-256 checksums.

### Safety

- Without a terminal (how agents run commands), `set` can only add new entries; changing or
  removing an entry needs your confirmation at a terminal.
- A value piped to `--field f=-` loses one trailing newline (`gh auth token | agv set ...` works).
  Files read with `@path` stay byte-exact.
- When stdin carries a value, confirmations are asked on the controlling terminal.
