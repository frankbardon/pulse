---
id: U38
slug: skill-sync
title: "Hand-written skills say exactly what the engine does, and every skill fits its budget"
track: Guided analysis
size: M
status: not-started
depends_on: [U21]
soft_depends_on: []
blocks: [U32]
todo_items: [235, 236, 237]
branch: skill-sync
---

# U38 — skill-sync

**Outcome:** Hand-written skills say exactly what the engine does, and every skill fits its budget.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U21](U21-guidance-generated-docs.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

U09 and U10 found hand-written atomic skills whose claims drifted from the registries and the runtime, and left 111 `op-*` bodies over the soft atomic budget. U21 generated the `## Use when` and `## Reading the output` sections and measures the budget on the hand-written body only, so the drift and the overrun are now the whole remaining skill debt. This unit corrects each claim against the engine, trims the over-budget bodies, flips the `op-*` budget from soft to hard, and closes two discovery leftovers from U10.

Numbering note: appended as U38 after U37, not renumbered.

## References

**Read before starting:**
- `.claude/reference/skill-pack.md`: Generated sections, budgets (the hand-written measure `skills.RenderGenerated(RenderFences(raw), nil)`), required sections
- [U09 handed on](U09-guidance-backfill-descriptive.md#handed-on) and [U10 handed on](U10-skill-ontology.md#handed-on): the drift list
- `go test ./internal/skills/ -run TestSkillTokenBudget -v` for the current over-budget list

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#235** (7. Guided analysis › Follow-ups from U21) Correct the stale hand-written skill claims against the engine: `op-agg-ci-*` calls `t_critical` a scaled critical value; `ATTR_REG_LEVERAGE` says range [0,1] but can exceed 1 outside the fit; `op-win-*` say `order_by` numeric/date while every type except `set_*` is admitted; `WIN_LAG` offset says at least 1 but predict accepts 0; `WIN_LAG` / `WIN_LEAD` say float64 output but copy the raw cell; `GROUP_RANGE` / `ROUNDED` declare `interval` required but default to 1
- [ ] **#236** (7. › Follow-ups from U21) Trim the `op-*` bodies over the 1,200-character atomic budget (about 111 today, measured on the hand-written body), then flip `TestSkillTokenBudget` for the `op-*` family from soft to hard; coordinate the `kind: design` and `session-bootstrap.md` overrun with U32 #161
- [ ] **#237** (7. › Follow-ups from U21) Discovery leftovers from U10: CLI `pulse skills` / `pulse examples` list embedder additions (or state they are the embedded library), reword toolmeta `DescExamplesSearch` / `DescExamplesGet` ("embedded library"), and tag the synth examples or drop the `op-synth-*` `## See tags=[synth]` lines that match no example

## Scope

**In scope**
- Verify each #235 claim against the engine, fix the skill (not the engine), and add a regression test where the claim is machine-checkable (a registry or manifest assertion)
- Trim bodies by moving detail into the generated sections' neighbours or the design skills, never by dropping a required section
- The hard `op-*` budget gate, with the measure U21 defined
- #237, including the facade or CLI decision on instance-scoped listing

**Out of scope**
- Engine behaviour changes (U35 and U36 own the runtime defects the skills currently describe)
- The `kind: design` and `session-bootstrap.md` overrun (U32 #161)
- New operators or new generated sections

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(skill-sync/E<n>-S<m>): …`; close each epic with `milestone(skill-sync/E<n>): vertical slice complete — <epic title>`.

### E1 — Skills match the engine
- S1: the #235 claims, each verified and fixed with a guarding test
- S2: #237 discovery leftovers

### E2 — Every skill fits its budget
- S1: trim the over-budget `op-*` bodies
- S2: flip the `op-*` budget to hard

## Acceptance criteria

- [ ] Every #235 claim is verified against the engine and the skill is corrected or the claim is shown true
- [ ] `go test ./internal/skills/ -run TestSkillTokenBudget` fails an `op-*` body over budget (hard)
- [ ] No `pulse skills` / `pulse examples` wording claims to list more or less than it does
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillTokenBudget` (op family hard)
- `TestAtomicSkillHasRequiredSections`, `TestSkillsCoverFeatureFences` unchanged and green
- `TestSkillPurposeSectionsCurrent` unchanged

## Update Demand companions

- `.claude/reference/skill-pack.md` (budget table: `op-*` hard)
- `docs/src/internals/` skill-pack notes if they quote the soft budget
- Regenerate `docs/src/guide` (`make docs`) when a skill body changes; skill edits also move the per-fixture manifest goldens

## Human inputs & decisions

- None.
