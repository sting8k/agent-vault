# US-m5a8 Secret groups: NAME/... paths, grouped list, typo suggestions, agent naming guide, webhook prefix filters

<!-- Status, lane, verify command, and proof live in .harness/stories/US-m5a8.json.
     Read them with `just-harness-cli query stories`. Do not copy them here. -->

## Goal

A user with secrets for many projects and environments in one vault can see which secret is for
what, and agents name, find and choose secrets on their own. The user asked for organisation only,
no scoping (D-eas1), and for typo suggestions that are good and a syntax for writing and looking up
that is handy, easy and transparent.

## Acceptance

- A name is one or more segments `[A-Z][A-Z0-9_]*` joined by `/` (`SHOP/PROD/DB`). Names without
  `/` stay valid; an existing vault works unchanged.
- The full name is the only name, everywhere: `set`, `rm`, `{{NAME.field}}`, `{{file:NAME.field}}`,
  `--env-from`, `[REDACTED:NAME.field]`, `audit.log`, webhook bodies. No short or contextual names,
  nothing tied to a directory or repository.
- `agv list` shows full names in name order, so each group stays together (heading lines dropped,
  user 2026-10-27); the filter still matches name and description, case-insensitively; `--json`
  keeps full names.
- Every mistake below gives the right name to copy, names only, never a value (G-7n9t):
  - a misspelled name (works today; keep it);
  - wrong case (`shop/prod/db`) gives the uppercase name;
  - the last segment alone (`DB`) gives every entry ending in it, not cut at 3;
  - a `{{...}}` in `run` that would name a stored secret but for its case is an error with the
    right placeholder, while other tools' template text (`{{.ID}}`, `{{ .Values.x }}`, `{{value}}`
    with no such secret) still passes untouched;
  - `set` of a name whose group is new and close to an existing group (`SHPO/` vs `SHOP/`) asks at a
    terminal and does not create the group silently without one;
  - `list` with a filter that matches nothing suggests close groups.
- `agv set` with no NAME shows the existing groups when it asks for the name.
- A webhook `secrets` filter takes a name, `NAME.field`, a group prefix ending in `/` (`SHOP/PROD/`)
  and `*` as one whole segment (`*/PROD/`). Anything else is a config error.
- `agv skills` tells agents to propose names as `PROJECT/ENV/SERVICE` with ENV one of `PROD`,
  `STAGING`, `DEV`, a description naming the service, environment and account, to choose with
  `agv list <project>` and the descriptions, and to follow the suggestion in an error.

## Scope

- In: name grammar (`vault/validate.go`, `inject/build.go`, `audit/config.go`), suggestions
  (`vault/notfound.go` and callers), `list`, `set`, webhook filter, `internal/skill/SKILL.md`,
  `docs/design.md`, README.
- Out: scoping by directory or repository, short names, labels or tags, `agv mv`, CHANGELOG
  (written at release), any push.

## Proof

Verify command: gofmt, vet, `go test -race ./...`. One test per risk: a grouped name round-trips
(set, inject, redaction label, audit record); a case-only near-miss `{{...}}` is an error while
foreign template text passes (fails on the old code); the last-segment suggestion lists every match;
the new-group near-miss is not created silently without a terminal; the webhook prefix and `*`
filters. Manual: a run in a temp `AGV_HOME` (set on the same command line as agv) showing the list
tree and each mistake's message.

## Design

Decided while implementing; `docs/design.md` ("Names and groups") is the living contract.

**Grammar.** `PART(/PART)*`, `PART = [A-Z][A-Z0-9_]*`. It stays written once per package
(`vault.nameRE`, `inject.placeholder`/`entryName`, `audit.secretRef`) because `inject` and `audit`
may not import `vault`. One cli test (`TestGroupedNameRoundTrips...`) drives a grouped name through
set, run, the redaction label, the audit record, a webhook filter and a webhook URL template, so the
copies cannot drift apart unseen.

**What else `/` touched.** Checked, not assumed:
- AES-GCM additional data `NAME.field`: unambiguous (neither part holds `.`); a test moves a value
  between `SHOP/PROD/DB` and `SHOP/STAGING/DB` and it fails to decrypt.
- Temp files: **broken** by `/` (`filepath.Join(dir, label)` made a directory that did not exist).
  Now the label's `/` becomes `.`; no name holds `.`, so the file name is unique.
- Env names come from fields, JSON output does not escape `/`, redaction labels and audit records
  are plain strings, and the tree relies on `/` sorting before every other name character.
  No change needed.

**Mistyped `{{shop/prod/db}}` vs foreign text.** A second regexp finds the same shape in any
letter case. If the text is not an exact placeholder, it is an error only when its corrected form
(name upper case, field lower case, `value` when absent) resolves to a stored entry **and** field;
otherwise it passes through. So `{{.ID}}`, `{{ .Values.x }}`, `{{value}}` with no secret VALUE, and
`{{db.nofield}}` are untouched. Cost accepted: a foreign `{{token}}` next to a stored `TOKEN` is an
error; `{{ token }}` (spaces) gets past it and is documented. Chosen over "any near-miss is an
error", which would break template text for everyone, and over "warn only", which an agent would not
see in a command that still runs. Not covered: `{{aws_prod}}` for an entry with no `value` field
passes through (its upper-case form would not resolve either).

**Suggestions** (`vault.Suggest`, first rule that finds anything decides): same letters with another
case or separator; every name with the typed parts in order (suffix matches first, not cut);
otherwise up to 3 names that differ in exactly one part by a typo. Parts under 3 letters allow no
edit except a swap (`BD` for `DB`), under 6 one edit, else two. The part-wise rule is deliberate:
the old whole-string distance (measured on the previous code) offered `SHOP/PROD/DB` for a missing
`SHOP/PRE/DB`, and an agent that "follows the suggestion" would run against production. Output is stored names only. A lookup error
never repeats text that is not a stored name; `set` and `list` do the same.

**New group near an existing one.** `vault.NewGroupNear` finds the first group of the new name that
does not exist and compares it with its siblings (same parent) by the typo rule. At a terminal:
y/N, default no. Without one: refuse, name the existing group and the corrected name, and offer
`--new-group` or a person at a terminal. The flag is the one addition to the CLI surface; it lets an
agent that really means `SHIP/` next to `SHOP/` proceed without a terminal, and it costs one
deliberate word, so a typo is not created silently. A new environment (`DEV` beside `PROD`) or a
group that resembles nothing is never asked about.

**List.** Full names in name order, no heading lines (the user dropped them as repetition,
2026-10-27): what is on screen is what to type, and an agent never joins `SHOP/` + `PROD/` + `DB`.
A filter that matches nothing prints close groups (names if no group); with `--json` that hint goes
to stderr so stdout stays JSON.

**Webhook filters.** `NAME`, `NAME.field`, a prefix ending in `/` (any depth), and `*` for one whole
part in any of them (`*/PROD/`, `*/PROD/DB.url`). A lone `*` and `*.field` are errors (they would
only match flat names, a trap); everything else is a config error.
