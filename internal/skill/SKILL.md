---
name: agv
description: Use when a shell command needs a secret (API token, password, private key, database URL, cloud credentials) that the user keeps in agv, or when the user mentions agv or agent-vault. agv runs the command with the secret injected and its output redacted; you never see the value.
---

# agv

No agv command prints a secret value. You name a secret; agv injects it into one command.

## Flow

1. `agv list [filter] [--json]` shows names, descriptions, fields, env names and file flags:

   ```text
   AWS_PROD (aws): AWS prod, read-only S3
     access_key_id      env AWS_ACCESS_KEY_ID
     secret_access_key  env AWS_SECRET_ACCESS_KEY
   DEPLOY_KEY (ssh-key): staging deploy key
     key  file
   GITHUB (api-token): GitHub PAT, repo read
     value
   ```

2. `agv run [flags] -- CMD ARGS...` runs CMD with them. Always write the `--`.

`list`, `run` and `skills` never prompt. `set` and `rm` are for the user: do not run them.

## Injecting (prefer env and file; args show in `ps`)

- Env: `--env-from NAME` sets every field of NAME that has an env name. `--env VAR='{{NAME.field}}'` sets one variable.
- File, for keys and certificates: `{{file:NAME.field}}` is the path of a private temp file, deleted when CMD exits. Under `--env-from`, a `file` field gets that path.
- Arg, last resort: `{{NAME.field}}` is the raw value.

`{{NAME}}` means `{{NAME.value}}`. Placeholders work in args after CMD and in `--env` templates, not in CMD itself; other `{{...}}` text (docker's `{{.ID}}`) is left alone. Setting one variable twice is an error.

```sh
agv run --env-from AWS_PROD -- aws s3 ls
agv run --env GH_TOKEN='{{GITHUB}}' -- gh api user
agv run --env PGPASSWORD='{{DB.password}}' -- psql -h db.internal -U app -c 'select 1'
agv run --env GIT_SSH_COMMAND='ssh -i {{file:DEPLOY_KEY}}' -- git pull
agv run -- curl -H 'Authorization: Bearer {{GITHUB}}' https://api.github.com/user   # visible in ps
```

## Quoting

Single-quote every placeholder and template, so your shell hands it to agv untouched.

Variables agv sets exist only inside the command it starts. With `agv run --env GH_TOKEN='{{GITHUB}}' -- curl -H "Authorization: Bearer $GH_TOKEN" ...`, your own shell expands `$GH_TOKEN` first, finds nothing, and curl gets an empty token. Put `'{{GITHUB}}'` in the arg instead, or use a program that reads the variable itself (`gh`, `aws`, `psql`).

## Shells, pipes, time limits

- agv never runs a shell and refuses CMD = sh, bash, zsh, dash, fish, ksh, mksh, tcsh or csh: a shell can route secrets around redaction. Do pipes, `&&` and redirects in your own shell, around agv; they get redacted output. `--allow-shell` lifts the refusal; use it only if the user says so.
- CMD reads agv's stdin: `agv run --env-from DB -- psql < schema.sql` works.
- Use `set -o pipefail`, or the command after a pipe hides a failing agv: `set -o pipefail; agv run ... -- aws s3 ls | sort`.
- `--timeout 30s` is off by default. On expiry: SIGTERM, SIGKILL about 2 s later, exit 124. Use it for anything that could hang. Temp files vanish when CMD exits, so detached commands (`docker run -d -v`, `ssh -f`) cannot use them.

## Redaction is best-effort

Secrets in CMD's stdout and stderr (also their base64, URL-encoded and JSON-escaped forms) become `[REDACTED:NAME.field]`. Other transformations pass through, and so do values inside a longer encoded string, such as the Basic header of `curl -v`. Never echo, print, log or transform a secret on purpose; avoid verbose or trace flags and `env`/`printenv`.

## Missing secrets

If agv reports an unknown secret, compare the name with `agv list`; agv suggests close names. If it is not stored, ask the user to run `agv set NAME` in a separate terminal. Never ask for the value, and never read `~/.agent-vault/`.

## Exit codes

CMD's exit code, except 124 timeout, 125 agv error, 126 not executable, 127 not found, 128+N killed by signal N. CMD can return those too: agv's own failures print a line starting `agv: ` on stderr.
