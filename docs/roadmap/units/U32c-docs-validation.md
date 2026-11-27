---
id: U32c
slug: docs-validation
title: "An agent and a fresh reader succeed from the docs alone, and the findings are closed and signed off"
track: API & release
size: M
status: done
depends_on: [U32b]
soft_depends_on: []
blocks: [U33]
todo_items: [166, 167, 768, 775, 799, 800, 801]
branch: docs-validation
---

# U32c — docs-validation

**Outcome:** An agent and a fresh reader succeed from the docs alone, and the findings are closed and signed off.

**Track:** API & release · **Size:** M · **Depends on:** [U32b](U32b-docs-content.md) · **Unblocks:** [U33](U33-v1-release.md)

## Summary

The last gate before the release. Once U32 has built the checks and U32b has written and emptied the baselines, this unit tests the result on people and agents: an evaluation harness drives about twenty tasks over MCP and is kept as a regression set, and a fresh reader works Getting Started and two question guides unaided. Every failure is fixed; the findings log is closed and the maintainer signs off.

## References

**Theme documents (read before starting):**
- [docs-audit 00 — Plan](../v1.0.0-docs-audit/00-plan.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#166** (11. Documentation audit) Fresh-reader review and agent task evaluation over MCP (~20 tasks, kept as a regression set); every failure fixed
  - Agent half done 2026-11-27: run `internal/eval/runs/2026-11-27-r3` passes 20/20 (`go run ./internal/tools/evalscore internal/eval/runs/2026-11-27-r3`). Runs r1 (15/20), r2 (19/20) and r4 (a broken harness run) are kept as records. Fresh-reader half done 2026-11-27 (proxy reader, a Sonnet subagent given only the built docs and the binary): run 1 logged 17 rows (3 stuck), all fixed; run 2 completed Getting Started, Compare groups and Change over time with no stuck point. Its 2 minor rows were fixed.
- [x] **#167** (11. Documentation audit) Findings log closed (fixed or deferred with reason and issue) and maintainer sign-off recorded
  - Done 2026-11-27 (E6-S1): [`reviews/U32-docs-review.md`](../reviews/U32-docs-review.md) gives every U32a, U32b and U32c row a disposition. The two open U32c rows were filed as #802 and #803 under [U33](U33-v1-release.md). The review's sign-off section names the proxy-reader caveat; the owner signed off on 2026-11-27.
- [x] **#768** (12. Release v1.0.0) `SECURITY.md` sends reporters to GitHub private vulnerability reporting: the owner enabled it on 2026-11-26 (`enabled: true`), and the release gates in [U33](U33-v1-release.md) verify `enabled: true` before every tag
- [x] **#775** (11. Documentation audit) Restructure `.claude/reference/guided-analysis/explain-and-skills.md` for readability (dense one-paragraph bullets), keeping every rule and the slice ceiling
- [x] **#799** (11. Documentation audit) A facet overlay's unknown `params.field` is refused `PULSE_OVERLAY_FIELD_UNKNOWN`, as on every other host, at predict and runtime
- [x] **#800** (11. Documentation audit) Name the referent of every "byte-identical" in `.claude/reference/` and `docs/src/library/`, or keep the idiom by decision and state the convention once
- [x] **#801** (11. Documentation audit) Rewrite the remaining public GoDoc history prose ("today", "future work", "was") to state the current contract only

## Scope

**In scope**
- An agent evaluation harness over MCP (scripted, about 20 tasks) committed as a regression set
- Fresh-reader review of Getting Started and two question guides
- Fixes for every failure, in the owning surface
- Findings log and maintainer sign-off

**Out of scope**
- New checks ([U32](U32-docs-audit.md)) and content passes ([U32b](U32b-docs-content.md))
- New features beyond what a finding needs. A gap that needs code is fixed in this unit (owner call: fix everything in-effort, e.g. `ListCohorts` and `pulse examples data`); only an open contract decision (#802) or a new capability outside the fix set (#803) is filed with an issue

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|docs|test(docs-validation/E<n>-S<m>): …`; close each epic with `milestone(docs-validation/E<n>): vertical slice complete — <epic title>`.

As built (one commit per story):

- **E1 — Maintainer can score answers to ~20 tasks** (3 stories): `internal/eval` (task format, checker, reference runner, intent coverage), the task set over every intent, the `evalscore` tool and the [agent-eval procedure](../../src/contributing/agent-eval.md)
- **E2 — Agents succeed over MCP** (3 stories): run r1, fixes for every failure, the green rerun committed
- **E3 — A fresh reader succeeds** (2 stories): proxy reader run 1, fixes (`pulse examples data` among them) and the clean rerun
- **E4 — Carried cleanups** (3 stories): #775, #800 with the CLAUDE.md manifest wording, #801
- **E5 — Owner items** (2 stories): #799; the Parquet u4 marker test and #768
- **E6 — Findings closed and signed off** (1 story): the [U32 docs review](../reviews/U32-docs-review.md) and this close

## Acceptance criteria

- [x] The fresh reader completes Getting Started and two question guides without outside help (proxy reader run 2, 2026-11-27)
- [x] The agent task evaluation passes every task and the task set is committed (run 2026-11-27-r3, 20/20)
- [x] Every finding is fixed or deferred with a reason and an issue link; maintainer sign-off recorded (in [`reviews/U32-docs-review.md`](../reviews/U32-docs-review.md); signed off by the owner on 2026-11-27)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- The agent task evaluation (scripted; kept for future releases)

## Update Demand companions

- `docs/roadmap/TODO.md` (tick #166, #167; file #802, #803 under U33)
- `docs/roadmap/reviews/U32-docs-review.md` (the consolidated findings log and sign-off)

## Human inputs & decisions

- Maintainer sign-off: the sign-off section of [`reviews/U32-docs-review.md`](../reviews/U32-docs-review.md), filled by the owner
- A fresh reader (developer without a statistics background): run as a proxy, an AI subagent given only the built docs and the binary; the review records this caveat

## Handed on from U32b

Found in docs-content (E6-S4, E8-S5); full text in [`TODO.md`](../TODO.md); tick there when delivered.

- [x] **#775** — **`explain-and-skills.md` is accurate but hard to read**; restructure it without losing a rule or raising the ceiling.
- [x] **#800** — **"byte-identical" leaves its referent implicit** in the contributor reference pages; name it, or keep the idiom by decision.
- [x] **#801** — **public GoDoc still says "today" / "future work"**; rewrite those comments to the current contract.

## Notes

- Split from U32 on 2026-11-24, following the U02 → U02b / U02c precedent.
