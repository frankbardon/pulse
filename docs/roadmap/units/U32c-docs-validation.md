---
id: U32c
slug: docs-validation
title: "An agent and a fresh reader succeed from the docs alone, and the findings are closed and signed off"
track: API & release
size: M
status: not-started
depends_on: [U32b]
soft_depends_on: []
blocks: [U33]
todo_items: [166, 167, 775]
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

- [ ] **#166** (11. Documentation audit) Fresh-reader review and agent task evaluation over MCP (~20 tasks, kept as a regression set); every failure fixed
- [ ] **#167** (11. Documentation audit) Findings log closed (fixed or deferred with reason and issue) and maintainer sign-off recorded
- [ ] **#775** (11. Documentation audit) Restructure `.claude/reference/guided-analysis/explain-and-skills.md` for readability (dense one-paragraph bullets), keeping every rule and the slice ceiling

## Scope

**In scope**
- An agent evaluation harness over MCP (scripted, about 20 tasks) committed as a regression set
- Fresh-reader review of Getting Started and two question guides
- Fixes for every failure, in the owning surface
- Findings log and maintainer sign-off

**Out of scope**
- New checks ([U32](U32-docs-audit.md)) and content passes ([U32b](U32b-docs-content.md))
- New features. Gaps that need code are logged and deferred with an issue unless trivial

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|docs|test(docs-validation/E<n>-S<m>): …`; close each epic with `milestone(docs-validation/E<n>): vertical slice complete — <epic title>`.

### E1 — Agents succeed over MCP
- S1: the evaluation harness and task set
- S2: fixes until every task passes; the task set committed for future releases

### E2 — A fresh reader succeeds
- S1: fresh-reader review; fixes
- S2: findings log closed; sign-off

## Acceptance criteria

- [ ] The fresh reader completes Getting Started and two question guides without outside help
- [ ] The agent task evaluation passes every task and the task set is committed
- [ ] Every finding is fixed or deferred with a reason and an issue link; maintainer sign-off recorded
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- The agent task evaluation (scripted; kept for future releases)

## Update Demand companions

- `docs/roadmap/TODO.md` (tick #166, #167)

## Human inputs & decisions

- Maintainer sign-off; a fresh reader (developer without a statistics background) for the review

## Handed on from U32b

Found in docs-content (E6-S4); full text in [`TODO.md`](../TODO.md); tick there when delivered.

- [ ] **#775** — **`explain-and-skills.md` is accurate but hard to read**; restructure it without losing a rule or raising the ceiling.

## Notes

- Split from U32 on 2026-11-24, following the U02 → U02b / U02c precedent.
