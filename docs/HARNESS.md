# Harness

This repository keeps shared project knowledge where every person, agent, and session can find it:
what is in progress, what is done and proven, which rules and decisions bind future work. Use it to
stay inside the user's scope and direction. Use only the parts the current task needs.

## Where things live

| Kind | Location | Who writes it |
| --- | --- | --- |
| Progress, proof, waivers (stories) | `.harness/stories/*.json` | CLI only |
| Decisions later work must inherit | `.harness/decisions/*.json` + `docs/decisions/<ID>-*.md` | CLI creates, you write the prose |
| Standing project rules (guardrails) | `.harness/guardrails/*.json` | CLI only |
| Retained evidence (traces) | `.harness/traces/*.json` | CLI only |
| Work packet prose (goal, acceptance, scope, proof) | `docs/stories/<ID>-*.md` | you |
| Behavior contracts (API, schema, UX, ops) | wherever the project owns them | you |

`.harness/` is committed and is the single source of truth for state. Never edit it by hand; run
`.harness/bin/just-harness-cli check` if it may be inconsistent (for example after a merge).
Prose files carry no status: status is read from the CLI.

## Start of a session

1. Read the user's request, then run `.harness/bin/just-harness-cli query status`.
   It shows active guardrails, open stories, items needing attention, and proposed decisions.
2. If the task continues an open story, read its packet before touching code.
3. Guardrails are binding. If the request conflicts with one, say so and ask before proceeding.

If the binary is missing (fresh clone), state is still readable: `cat .harness/*/*.json`.
Reinstall the binary with the one-liner in the project README before writing state.

## Default flow

```text
Understand -> Implement -> Verify -> Report
```

1. **Understand** the requested outcome, the affected code, and the contract that owns the behavior.
   Read the smallest context that answers the question; widen only when risk or uncertainty requires it.
   For a bug, name the invariant that was violated before fixing it.
2. **Implement** the smallest change that fits the existing design. Stay inside the requested scope;
   ask before expanding it.
3. **Verify** with executable proof proportional to the lane (below).
4. **Report** the outcome, the evidence, and anything not verified, skipped, or waived.

Never claim behavior works without evidence. A waived or skipped check is not a passed check.

## Lanes

Pick a lane by what failure would cost, not by business priority. The lane sets proof depth.

- **tiny**: narrow, low-risk edit with an obvious contract (copy, names, local docs, small fixes).
  Focused check, then report.
- **normal**: bounded behavior change with understood blast radius. Direct proof plus relevant
  regression checks; keep adjacent contracts intact.
- **high_risk**: failure could hurt security, data, public contracts, external systems, or broad
  existing behavior. Read the owning contract and relevant decisions first; make acceptance and
  validation explicit; ask before implementing when direction is ambiguous.

High-risk triggers (any one is a strong signal):

- authentication, authorization, tenant or role boundaries;
- data loss, migrations, retention, ownership;
- privacy, secrets, audit, sensitive access;
- payments, email, queues, webhooks, third-party side effects;
- public API or client-visible contract changes;
- weakening or removing validation;
- broad changes to established, test-covered behavior.

## When to create state

Most work needs no records. Create one only when it serves a need below.

| Create | When |
| --- | --- |
| Story (`story add`) | Acceptance must be tracked, work spans sessions or actors, several steps need coordination, or the lane is high_risk. |
| Decision (`decision add`) | The work settles a choice (behavior, architecture, auth, data ownership, public contract, validation) that future work must inherit. Routine implementation choices do not qualify. |
| Guardrail (`guardrail add`) | The user states, or the work establishes, a standing rule every future change must follow. |
| Trace (`trace`) | Evidence must outlive the session: release, handoff, failure attribution, durable acceptance. |

Do not create placeholder records or restate what the diff and tests already show.

## Completion contract

Before reporting work complete, check each clause. A clause that does not apply creates nothing.

- **Owning documentation**: behavior, schema, architecture, or operator usage changed → update the doc
  that owns that contract.
- **Durable decision**: a consequential choice was settled → record or update one decision.
- **Durable evidence**: retained proof is needed → record one trace, including what was not verified.

## Stories and the completion gate

```sh
.harness/bin/just-harness-cli story add --title "..." --lane normal --verify "<test command>"
.harness/bin/just-harness-cli story update --id US-xxxx --status in_progress
.harness/bin/just-harness-cli story verify --id US-xxxx          # repeat until it passes
.harness/bin/just-harness-cli story update --id US-xxxx --status implemented
```

- A story with a verify command can become `implemented` only after its current command passed.
- A `high_risk` story without a verify command cannot close without a waiver.
- `--waive "reason"` closes without a pass. Use it only when proof is genuinely unavailable, and
  state the waiver in your report. Waivers stay visible in `query status`.
- Changing the verify command, or moving a story to `changed`, discards the previous pass.
- The gate proves the command passed. It does not prove the command tests the right thing: choose a
  verify command that exercises the acceptance criteria and the owning contract.

## Handoff

When work crosses a session or actor boundary, the story packet is the handoff contract. Keep its
acceptance, current state, evidence, open gaps, and next action current. Work returned by a delegate
is provisional until the integrating actor verifies it; only then mark the story `implemented`.
Commit `.harness/` together with the code it describes.

## Source order

When sources disagree, prefer, in order:

1. the user's current request;
2. active guardrails;
3. owning contract docs and accepted decisions;
4. story packets;
5. existing code and tests.

After implementation, the contract docs plus executable tests are the living contract.
