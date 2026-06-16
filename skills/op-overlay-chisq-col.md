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

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `column`. |
| `Ref` | object | (empty) | Implicit-margin — leave empty. Any populated arm rejected. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: implicit-margin χ² (no `Ref`). Compatible with any crosstab regardless of cell aggregator; reads observed × expected from row/column margins recomputed by the buffered orchestrator.

## Output

SERIES — `OverlayLayer.Payload.Shape = "series"`. One `SeriesEntry` per column key carrying `Summary.Statistic` (χ² value), `Summary.PValue`, `Summary.Parameters["df"]` = `rows - 1`. Layer `Baseline` unset (inferential).

## Gotchas

- Reuses `chiSquareSurvival` — byte-equal p-values to `TEST_CHISQ` and `OVERLAY_CHISQ_ROW` / `OVERLAY_CHISQ_MATRIX` on the same contingency.
- Any `expected < 5` in a column emits ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per offending column.
- Absent host cell treated as observed count of 0.
- Scope MUST be `column`. Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent — host crosstab path always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-row`, `op-test-chisq`.
