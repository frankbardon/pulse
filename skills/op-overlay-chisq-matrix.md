---
name: op-overlay-chisq-matrix
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_MATRIX
description: Whole-matrix χ² independence test across the host crosstab's row × column contingency table.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `matrix`. |
| `Ref` | object | (empty) | Implicit-margin — leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). First inferential MATRIX-host overlay; establishes the SCALAR-payload pattern E2/E5 χ²/post-test kinds reuse.

## Output

SCALAR — `OverlayLayer.Payload.Shape = "scalar"`. `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}` where `df = (rows-1)*(cols-1)`. Layer `Baseline` unset (inferential — no ratio centerpoint).

## Gotchas

- Expected cell formula: `row_margin × col_margin / grand_total`. p-value via `chiSquareSurvival` — byte-equal to `TEST_CHISQ` on the same contingency.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` warning per layer.
- Absent host cell treated as observed count of 0.
- Scope MUST be `matrix`. Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent — margins recomputed from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-row`, `op-overlay-chisq-col`, `op-test-chisq`.
