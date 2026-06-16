---
name: op-overlay-chisq-row
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_ROW
description: Per-row χ² goodness-of-fit test across the host crosstab's contingency table.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `row`. |
| `Ref` | object | (empty) | Implicit-margin — leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: implicit-margin χ² (no `Ref`). Compatible with any crosstab regardless of cell aggregator; row-axis twin of `OVERLAY_CHISQ_COL`.

## Output

SERIES — `OverlayLayer.Payload.Shape = "series"`. One `SeriesEntry` per row key carrying `Summary.Statistic` (row χ²), `Summary.PValue`, `Summary.Parameters["df"]` = `cols - 1`. Entries align element-for-element with host `RowKeys`. Layer `Baseline` unset.

## Gotchas

- Reuses `chiSquareSurvival` — byte-equal p-values to `TEST_CHISQ` / `OVERLAY_CHISQ_MATRIX` / `OVERLAY_CHISQ_COL` on the same contingency.
- Any `expected < 5` in a row emits ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per offending row.
- Absent host cell treated as observed count of 0.
- Scope MUST be `row`. Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-col`, `op-test-chisq`.
