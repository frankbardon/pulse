---
id: U75
slug: zones-labels-2
title: "Cohort default zone, label binding and attribute edge cases land with their parity proofs."
track: Time zones
size: M
status: not-started
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [480, 481, 482, 483, 484, 485, 714]
branch: zones-labels-2
---

# U75 — zones-labels-2

**Outcome:** Cohort default zone, label binding and attribute edge cases land with their parity proofs.

**Track:** Time zones · **Size:** M · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first)

## Summary

Time-zone and label depth: a cohort-level default zone, per-row zone runtime equivalence through joins and chains, a zero-spread attribute warning, label bindings on convert and on numeric fields, and a sorted-input fast path for the previous-state attribute.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#480** A cohort-level default zone: a schema-declared zone column (a field the schema marks as the row's IANA zone) so every request inherits it with no `time_zone_field` on each slot. Needs a `0x02` schema section or a sidecar, so it is a byte-layout change. Interview decision in U50: v1.x backlog · [U75](units/U75-zones-labels-2.md)
- [ ] **#481** Per-row zone runtime equivalence through joins and chain stages ≥1: only the refusal tests exist today (a grouped-cohort `0x02` filter precompute is covered by a unit test only). Found in U50 (E1-S2) · [U75](units/U75-zones-labels-2.md)
- [ ] **#482** `ATTR_ZSCORE` / `ATTR_TSCORE` / `ATTR_NORMALIZED` over a field with zero spread score every row null with only the aggregation's `n_null` as a sign; the multivariate attributes warn `PULSE_MATRIX_SINGULAR`. Consider a warning (a new or reused code). Found in U50 (E7-S2) · [U75](units/U75-zones-labels-2.md)
- [ ] **#714** A predict advisory for a nullable read fed to arithmetic or a function in an expression (`ATTR_FORMULA` `x * 2` over a nullable `x`): the runtime aborts the run with `PROCESSING_RUNTIME` on the first null row, while predict says nothing. `PULSE_ADVISORY_FORMULA_NULLABLE_RESULT` (#475) covers only a nil reaching the RESULT position, by design. Decide the code (a sibling advisory or a widened one), reuse `exprident`'s guard analysis (`??`, `== nil` / `!= nil` ternaries) and pin fired ⇔ the run aborts on a null row. Found in U67 (E5-S5) · [U75](units/U75-zones-labels-2.md)
- [ ] **#483** `Pulse.PredictConvert` facade and a `pulse convert --labels` flag: library `ConvertJob.Predict` without a resolver cannot raise `PULSE_LABEL_KEY_INVALID`, and the CLI cannot bind labels on a convert. Found in U50 (E6-S2) · [U75](units/U75-zones-labels-2.md)
- [ ] **#484** Numeric label bindings on process / sample / facet / chain responses: `descriptor.ValidateLabels` still refuses a numeric field (replace mode would turn a number into a string, so it is a design call). Found in U50 (E3-S2) · [U75](units/U75-zones-labels-2.md)
- [ ] **#485** `ATTR_PREVIOUS_STATE` sorted-input O(subjects) fast path: a cohort already ordered by subject then instant is scored in one pass with no per-subject sort. Found in U55 (E1-S1) · [U75](units/U75-zones-labels-2.md)
