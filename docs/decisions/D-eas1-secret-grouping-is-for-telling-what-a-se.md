# D-eas1 Secret grouping is for telling what a secret is for; no scoping by working directory

<!-- Status lives in .harness/decisions/D-eas1.json. Change it with `just-harness-cli decision update`. -->

## Context

One user keeps secrets for many projects and environments (several prods, staging, personal) in
one vault. The lead proposed binding projects to directories, so that a project's secrets work only
inside its directories and short names resolve by the current directory. The user rejected that
(user, 2026-10-27): the vault is global, so strict per-directory scoping adds friction without a
real boundary. What he wants is to see which secret is for what.

## Decision

Grouping secrets is for organising and choosing (by people and agents) only. Every secret stays
usable from any directory, under its one full name.

## Consequences

- Future work must not tie secrets to working directories, git repositories or projects, and must
  not resolve names by context (no short or relative names) unless the user reopens this.
- How groups are written (naming convention, `/` in names, or a field) is still open.
