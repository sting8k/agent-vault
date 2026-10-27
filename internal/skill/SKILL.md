---
name: agv
description: Use when a shell command needs a secret (API token, password, private key, database URL, cloud credentials) that the user keeps in agv, or when the user mentions agv or agent-vault. agv runs the command with the secret injected and its output redacted; you never see the value.
---

# agv

You name a secret; agv injects it into one command. No agv command prints a value.

## Flow

1. `agv list [filter] [--json]` shows names, descriptions, fields, env names and file flags, in
   name order, so a group such as `SHOP/PROD/` stays together:

   ```text
   AWS_PROD (aws): AWS prod, read-only S3
     access_key_id      env AWS_ACCESS_KEY_ID
     secret_access_key  env AWS_SECRET_ACCESS_KEY
   DEPLOY_KEY (ssh-key): staging deploy key
     key  file
   GITHUB (api-token): GitHub PAT, repo read
     value
   SHOP/PROD/DB (db-url): Shop prod Postgres, main account
     url  env DATABASE_URL
   SHOP/STAGING/DB (db-url): Shop staging Postgres, main account
     url  env DATABASE_URL
   ```

   `agv list shop` keeps the entries whose name or description contains it, so list one project
   (`agv list SHOP/`) or one environment (`agv list /PROD/`) and read the descriptions to choose.

2. `agv run [flags] -- CMD ARGS...` runs CMD with them. Always write the `--`.

## Injecting (prefer env and file; args show in `ps`)

- Env: `--env-from NAME` sets every field of NAME that has an env name. `--env VAR='{{NAME.field}}'` sets one variable.
- File, for keys and certificates: `{{file:NAME.field}}` is the path of a private temp file, deleted when CMD exits. Under `--env-from`, a `file` field gets that path.
- Arg, last resort: `{{NAME.field}}` is the raw value.

`{{NAME}}` means `{{NAME.value}}`; always write the whole name (`{{SHOP/PROD/DB.url}}`). Placeholders work in args after CMD and in `--env` templates, not in CMD itself; other `{{...}}` text (docker's `{{.ID}}`) is left alone.

```sh
agv run --env-from AWS_PROD -- aws s3 ls
agv run --env GH_TOKEN='{{GITHUB}}' -- gh api user
agv run --env PGPASSWORD='{{SHOP/STAGING/PG.password}}' -- psql -h db.internal -U app -c 'select 1'
agv run --env GIT_SSH_COMMAND='ssh -i {{file:DEPLOY_KEY}}' -- git pull
agv run -- curl -H 'Authorization: Bearer {{GITHUB}}' https://api.github.com/user   # visible in ps
```

## Quoting

Single-quote every placeholder and template, so your shell hands it to agv untouched.

Variables agv sets exist only inside CMD: in `-- curl -H "Bearer $GH_TOKEN"` your shell expands `$GH_TOKEN` to nothing first. Put `'{{GITHUB}}'` in the arg, or use a program that reads the variable (`gh`, `aws`, `psql`).

## Shells, pipes, time limits

- agv refuses a shell as CMD (`bash -c ...`): it could route secrets around redaction. Do pipes, `&&` and redirects in your own shell, around agv. Use `--allow-shell` only if the user says so.
- CMD reads agv's stdin: `agv run --env-from DB -- psql < schema.sql` works.
- Use `set -o pipefail`, or the command after a pipe hides a failing agv: `set -o pipefail; agv run ... -- aws s3 ls | sort`.
- `--timeout 30s` (off by default) for anything that could hang; expiry exits 124.
- Temp files vanish when CMD exits, so detached commands (`docker run -d -v`, `ssh -f`) cannot use them.

## Redaction is best-effort

Secrets in CMD's stdout and stderr (also their base64, URL-encoded and JSON-escaped forms) become `[REDACTED:NAME.field]`. Other transformations pass through, and so do values inside a longer encoded string, such as the Basic header of `curl -v`. Never echo, print, log or transform a secret on purpose; avoid verbose or trace flags and `env`/`printenv`.

## Missing secrets

An unknown name or a wrong-case placeholder fails with the stored names you probably meant and what to do. Before using a suggested name, check its project and environment against its description in `agv list`; never switch to another project or environment than the one you were given. If the secret is not stored and you were not given its value, ask the user to run `agv set NAME` in a separate terminal. Never ask for the value, and never read `~/.agent-vault/`.

## Adding a secret

If the user gives you a value to keep, add it yourself, on stdin or from a file, never as an argument. Changing or removing a stored secret is for the user at a terminal.

```sh
agv set SHOP/PROD/DB --type db-url --desc 'Shop prod Postgres, main account' --field url=- <<'EOF'
postgres://app:...@db.internal/shop
EOF
```

Stdin fills one field; give others as `--field f=@path` and delete the files after. Keep the value nowhere else (notes, memory, commits).

Name it `PROJECT/ENV/SERVICE` (`SHOP/STAGING/STRIPE`, `PERSONAL/GITHUB`), ENV one of `PROD`, `STAGING`, `DEV`; run `agv list PROJECT` first and reuse its spelling. The description names service, environment and account: agents choose by it.

If `agv set` refuses a new group close to an existing one (`SHPO/` next to `SHOP/`), use the existing group; add `--new-group` only after the user confirms the new spelling.

## Audit log

agv logs each `run`, `set` and `rm` for the user (names, never values) and may notify them. A stderr line `agv: audit log: ...` or `agv: webhook ...` is a problem in the user's setup; your command still ran. Tell the user; do not edit `~/.agent-vault/config.json`.

## Exit codes

CMD's exit code, except 124 timeout, 125 agv error, 126 not executable, 127 not found, 128+N killed by signal N. CMD can return those too: agv's own failures print a line starting `agv: ` on stderr.
