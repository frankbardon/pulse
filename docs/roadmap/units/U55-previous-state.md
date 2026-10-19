---
id: U55
slug: previous-state
title: "A long panel feeds a from-state × to-state crosstab: a per-subject previous-state attribute, read in local time"
track: Time zones
size: S
status: not-started
depends_on: [U50]
soft_depends_on: []
blocks: [U32]
todo_items: [452]
branch: previous-state
---

# U55 — previous-state

**Outcome:** A long panel feeds a from-state × to-state crosstab: a per-subject previous-state attribute, read in local time.

**Track:** Time zones · **Size:** S · **Depends on:** [U50](U50-local-time.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The `flows` overlays (`OVERLAY_MARKOV`, `OVERLAY_SYMMETRY`, `OVERLAY_RETENTION`) read a from-state × to-state crosstab, but a panel stored long (one row per subject per time point) has no `from` column: the caller must pre-pivot to wide `state_t1` / `state_t2` fields first. `WIN_LAG` / `WIN_LEAD` are numeric and run on result rows, so they cannot pair a record's earlier state with its current one. This unit adds `ATTR_PREVIOUS_STATE`: for each record, the state the same subject (a key field) held at its previous time point (ordered by a date / datetime field), or null for the subject's first record. It would be the fourth `flows` declarer.

The ordering field is read in the row's local time, so a datetime panel that spans zones orders and breaks ties the way the subject lived them. U50 delivered per-row zones (`time_zone_field` on a slot, resolved in `internal/temporal`), which this attribute reuses; it does no zone math of its own.

Numbering note: appended after U54, not renumbered. Handed on from [U30](U30-matrix-extensions-hardening.md) (E7-S2, #449), first filed under [U50](U50-local-time.md) and moved here when U50 closed. **Release intent:** v1.0.0.

## References

**Read before starting:**
- `.claude/reference/execution-modes.md` (Time zones, Date-family field types) and `skills/time-zones.md`
- `.claude/reference/guided-analysis.md` (a new operator needs a `Purpose`; `flows` is the intent it declares), `.claude/reference/update-demand.md`
- `skills/op-overlay-markov.md`, `skills/op-overlay-symmetry.md`, `skills/op-overlay-retention.md`, `skills/crosstab-guide.md`
- How an attribute that needs a pre-pass over all rows (the `TwoPassAttribute` family: `ATTR_ZSCORE`, `ATTR_PERCENTILE`) buffers, and how it declares itself non-fusable beside a crosstab

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#452** (Previous state) `ATTR_PREVIOUS_STATE`: a per-subject previous-state attribute. For each record it gives the state the same subject held at its previous time point (ordered by a date / datetime field, read in the row's local time), or null for the subject's first record; a long panel then feeds a from-state × to-state crosstab and the `flows` overlays without pre-pivoting

## Scope

**In scope**
- `ATTR_PREVIOUS_STATE`: subject key field, order field (date or datetime), state field; null for a subject's first record and for a null state or order cell
- Local-time ordering through the existing zone resolution, including per-row zones
- Behaviour on ties in the order field (deterministic, documented) and on a streaming request (non-streamable, declared in predict)
- A categorical output that a crosstab axis (`GROUP_CATEGORY`) and the `flows` overlays accept
- Atomic skill, capability, `Purpose` / `Interpretation`, example tag, `flows` declarer count, `make docs`

**Out of scope**
- A previous value of a numeric field (`WIN_LAG` on result rows covers it)
- Multi-step lags (the state two time points back)
- A schema-declared zone column (a v1.x backlog item filed by U50, #480)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(previous-state/E<n>-S<m>): …`; close each epic with `milestone(previous-state/E<n>): vertical slice complete — <epic title>`.

### E1 — Previous state of a subject
- S1: `ATTR_PREVIOUS_STATE` buffered, ordered by a date / datetime field, local-time aware; predict parity and streamability
- S2: the long-panel example: from × to crosstab into `OVERLAY_MARKOV` / `OVERLAY_SYMMETRY` / `OVERLAY_RETENTION`, no pre-pivot; skill, `Purpose`, docs

## Acceptance criteria

- [ ] A long panel's from × to crosstab equals the one built from the pre-pivoted wide `state_t1` / `state_t2` fields
- [ ] A subject's first record is null; a datetime panel across zones orders by local time (a per-row-zone test)
- [ ] `OVERLAY_MARKOV`, `OVERLAY_SYMMETRY` and `OVERLAY_RETENTION` run over the long panel with no copied numbers
- [ ] No zone math outside `internal/temporal`
- [ ] `format_version` stays `"1.1"`; every change is additive
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestOperatorHasAtomicSkill`, `TestSkillsCoverAllOperatorComponents`, `TestSkillsCoverAllPurposes`, `TestManifestAttributesComplete`, `TestEveryOperatorHasAnExampleTag`, `TestPredict_Streamable_MatchesRuntime`, `TestDocsGeneratedCurrent`
- New: panel equivalence against the wide form, first-record null, tie ordering, per-row-zone ordering, every execution mode (buffered, parallel decode, shard archive, fused crosstab declines)

## Update Demand companions

- `skills/op-attr-previous-state.md`, `capabilities_attributes.go`, an example tag, `Purpose` / `Interpretation`, `features.go`, the `flows` intent text, `make docs`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** the tie rule when two records of one subject share an order value (stable by cohort row order, or refuse as ambiguous).
