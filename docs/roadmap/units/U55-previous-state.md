---
id: U55
slug: previous-state
title: "A long panel feeds a from-state × to-state crosstab: a per-subject previous-state attribute"
track: Time zones
size: M
status: done
depends_on: [U50]
soft_depends_on: []
blocks: [U32]
todo_items: [452]
branch: previous-state
---

# U55 — previous-state

**Outcome:** A long panel feeds a from-state × to-state crosstab: a per-subject previous-state attribute.

**Track:** Time zones · **Size:** M · **Depends on:** [U50](U50-local-time.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The `flows` overlays (`OVERLAY_MARKOV`, `OVERLAY_SYMMETRY`, `OVERLAY_RETENTION`) read a from-state × to-state crosstab, but a panel stored long (one row per subject per time point) has no `from` column: the caller had to pre-pivot to wide `state_t1` / `state_t2` fields first. `WIN_LAG` / `WIN_LEAD` are numeric and run on result rows, so they cannot pair a record's earlier state with its current one. This unit adds `ATTR_PREVIOUS_STATE`: for each record, the state the same subject (`subject_field`) held at its previous time point (ordered by `order_field`, a date / datetime), or null for the subject's first record. It is the fourth `flows` declarer.

The attribute orders by INSTANT, so it is zone-independent: it is not zone-capable (no `tz` / `tz_field`) and does no zone math; a datetime panel spanning zones orders identically whatever the request zone. (The first draft read the order field in local time; the interview dropped that: an instant already orders a panel correctly.)

Numbering note: appended after U54, not renumbered. Handed on from [U30](U30-matrix-extensions-hardening.md) (E7-S2, #449), first filed under [U50](U50-local-time.md) and moved here when U50 closed. **Release intent:** v1.0.0.

## References

**Read before starting:**
- `.claude/reference/execution-modes.md` (Before-filters attributes, in `execution-modes/shards-and-merge.md`) and `.claude/reference/guided-analysis.md`
- `skills/op-attr-previous-state.md`, `skills/op-overlay-markov.md`, `skills/op-overlay-symmetry.md`, `skills/op-overlay-retention.md`, `skills/crosstab-guide.md`

**TODO items delivered by this unit** (ticked in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#452** (Previous state) `ATTR_PREVIOUS_STATE`: a per-subject previous-state attribute; a long panel feeds a from-state × to-state crosstab and the `flows` overlays without pre-pivoting

## Scope

**In scope (shipped)**
- `ATTR_PREVIOUS_STATE` (`field` = state, `subject_field`, `order_field`): null for a subject's first record and for a null subject, order or previous-state cell (no carry-forward)
- Computed over EVERY decoded record BEFORE the filter pass (a per-operator property; every other attribute still runs after filters), so a date window keeps each subject's first in-window record with its true previous state. `PredictResult.ComputedBeforeFilters` lists it. The Components floor `{n, n_null}` counts every scored record (owner: keep; `n + n_null` = `Run.total_records`)
- Output is a categorical column inheriting the state field's dictionary (a virtual field in the schema view: `prevstate.KeySchema`), so a `GROUP_CATEGORY` axis, `AGG_MODE_COUNT`, smart defaults, `Options.AutoLabels` bindings and label display all read it as the state field
- Expressions read it BY STATE NAME (`prev == "paid"`, `prev != nil`), predict and runtime alike (E2-S4); a field-keyed filter naming the label stays `SERVICE_VALIDATION`
- Materialize writes an ordinary `categorical_*` column (no byte-layout change); the SPSS sidecar carries the state variable's labels with `Observed` flags recomputed from the written column
- Weight class not-weightable (instance default weight skipped; an explicit weight is `PROCESSING_CONFIG`; `"weight": null` runs it). Buffered only: non-streamable, declines the fused crosstab, never meets the parallel reducer or shard merge; a buffered crosstab under `DecodeWorkers > 1` takes `ArmParallelDecode` and the attribute scores the full decoded set
- MCP bound per-type schema accepts the field types each operator takes (E2-S5; also fixes `ATTR_CODE_IN`, `ATTR_DATE_PART`, `ATTR_SET_HAS`)
- Example `overlays/55_crosstab_previous_state_flows.json` on the fixture pair `plan_history` (long) and `plan_history_wide`; parity tests prove the long-panel crosstab and the three `flows` overlays equal the wide form cell for cell
- Atomic skill, capability, `Purpose` / `Interpretation`, example tag, features row, recommend `flows` draft (bound `subject_field` prefers an id-like integer), `make docs`

**Out of scope**
- A previous value of a numeric field (`WIN_LAG` on result rows covers it); multi-step lags
- A schema-declared zone column (v1.x backlog, #480)

## Epics & stories

- E1 — Previous state of a subject: the operator (buffered compute, tie rule), predict parity and the categorical key view, features / Components / recommend wiring (S1–S3)
- E2 — Execution and parity: before-filters ordering, materialize + SPSS carry, the execution-mode parity matrix, expressions by state name (S4), MCP bound schema (S5)
- E3 — Flows readout from a long panel, and close-out: example and wide-twin parity, roots (chain, facet, sample), roadmap close (S1–S2)

## Acceptance criteria

- [x] A long panel's from × to crosstab equals the one built from the pre-pivoted wide `state_t1` / `state_t2` fields (cell- and label-for-label)
- [x] A subject's first record is null; ordering is zone-independent: the same result under any request zone and for a datetime panel spanning zones
- [x] `OVERLAY_MARKOV`, `OVERLAY_SYMMETRY` and `OVERLAY_RETENTION` run over the long panel with no copied numbers
- [x] No zone math outside `internal/temporal` (the attribute does none)
- [x] `format_version` stays `"1.1"`; every change is additive
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Update Demand companions

- `skills/op-attr-previous-state.md`, `capabilities_attributes.go`, an example tag, `Purpose` / `Interpretation`, `features.go`, the `flows` intent text, `make docs`
- `.claude/reference/execution-modes/shards-and-merge.md` (Before-filters attributes); CLAUDE.md untouched (no stated contract moved)

## Human inputs & decisions

- **Decided (interview):** the tie rule when two records of one subject share an order value is neither stable-by-row-order nor a refusal: tied rows each take the state at the last earlier distinct time point; the row after a tie is null only if the tied states differ (identical tied states give that state). Each such null counts toward `PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE` (a warning; `Strict` fails the slot with it).
- **Decided (interview):** zone-independent ordering by instant; the per-row-zone acceptance criterion was replaced.
- **Decided (owner, 2026-10-19):** expressions read the label by state name (E2-S4); the MCP bound schema is fixed here (E2-S5); the Components floor counts every scored record (keep).
- **Decided:** Markov key-set rule. The column axis keys every record, so a first-only state (e.g. `trial`) is an empty column, no refusal; a last-only state is column-only and must be listed in `absorbing`, else `PULSE_OVERLAY_MATRIX_NOT_SQUARE` (the wide form behaves the same).

## Shipped

Epics E1–E3 above; example `overlays/55_crosstab_previous_state_flows`; embedder notes in [Changes from U55](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u55-previous-state). Behaviour for the next alpha's release notes: the MCP bound schema widened for categorical / date / set attribute fields; the `OVERLAY_INDEX_VS_BASELINE` out-of-range message is one shared text on predict and runtime.

## Handed on

| Item | To | Why |
|---|---|---|
| #485 sorted-input O(subjects) fast path for `ATTR_PREVIOUS_STATE` | v1.x backlog (no unit) | the buffered compute sorts every record per subject; a cohort already ordered by subject then instant could be scored in one pass without the sort |
| #486 extension attributes declaring an output dictionary | v1.x backlog (no unit) | the categorical key view is built for the built-in only; an `extend` attribute emitting a categorical cannot yet declare the dictionary it inherits |
