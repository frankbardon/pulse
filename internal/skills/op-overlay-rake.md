---
name: op-overlay-rake
kind: operator
category: OVERLAY
operator: OVERLAY_RAKE
description: Rake a crosstab to target row and column margins (IPF) — fitted table, adjustment factors and a convergence report; the host is never changed.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` `matrix`. `ref` empty. `level`/`within` `0`.

| Name | Type | Default | Description |
|---|---|---|---|
| `row_targets` | object | (required) | `{key: value}`, one per host key, no extras. |
| `col_targets` | object | (required) | `{key: value}`, one per host key, no extras. |
| `targets_as` | enum | `shares` | `shares` (normalized, scaled to the host total) \| `counts` (totals must agree). |
| `max_iter` | int | `100` | Iteration cap, 1–100,000. |
| `tolerance` | float | 1e-08 | Convergence threshold on the maximum relative margin error, in `(0, 1)`. A bad or unknown param ⇒ `PULSE_OVERLAY_PARAM_MISSING`. |


## Host shape

MATRIX crosstab of count or sum cells.

## Output

Primary MATRIX: fitted table. Part `factors` (MATRIX fitted / observed; `null` where observed is 0). Summary params: `iterations`, `residual`, `converged`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Key mismatch, or `counts` totals differ ⇒ `PULSE_OVERLAY_RAKE_TARGET_MISMATCH` (never dropped).
- Structural zeros stay zero; an empty row cannot reach a positive target.
- Not converged ⇒ `PULSE_OVERLAY_RAKE_NOT_CONVERGED`, payload still emitted, `converged` 0.
- Bad (negative / undefined NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Rakes the table only; it writes no weights to the cohort.

## See

- Skills: `overlay-system`, `weighting`, `op-overlay-share-of-row`.
