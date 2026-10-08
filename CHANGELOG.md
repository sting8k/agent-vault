# Changelog

All notable changes to agv. Each release on GitHub shows the section for its tag.

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
