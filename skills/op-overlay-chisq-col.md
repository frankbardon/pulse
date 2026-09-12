---
name: op-overlay-chisq-col
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_COL
description: Per-column χ² goodness-of-fit test across the host crosstab's contingency table.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` (enum, required) — must be `column`. `Ref` (object, empty) — implicit-margin — leave empty. `Level`/`Within` must be `0`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Compatible with any crosstab regardless of cell aggregator; reads observed × expected from row/column margins recomputed by the buffered orchestrator.

## Output

SERIES — `OverlayLayer.Payload.Shape = "series"`. One `SeriesEntry` per column key carrying `Summary.Statistic` (χ² value), `Summary.PValue`, `Summary.Parameters["df"]` = `rows - 1`. Layer `Baseline` unset (inferential).

## Gotchas

- Reuses `chiSquareSurvival` — p-values byte-equal to `TEST_CHISQ`, `OVERLAY_CHISQ_ROW`, `OVERLAY_CHISQ_MATRIX`.
- Any `expected < 5` in a column emits ONE `PULSE_OVERLAY_EXPECTED_LOW` per offending column.
- Absent host cell treated as observed count of 0.
- Buffered (inherent — host crosstab path always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-row`, `op-test-chisq`.
