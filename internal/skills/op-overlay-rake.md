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

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `row_targets`, `col_targets` — `{key: value}`, one per host key, no extras.
- `targets_as` — `shares` (default; normalized, scaled to the host total) | `counts` (totals must agree).
- `max_iter` — default 100. `tolerance` — default 1e-8 (max relative margin error).

## Host shape

MATRIX crosstab of count or sum cells.

## Output

Primary MATRIX: fitted table. Part `factors` (MATRIX fitted / observed; `null` where observed is 0). Summary params: `iterations`, `residual`, `converged`.

<!-- generated: reading-the-output -->

## Gotchas

- Key mismatch, or `counts` totals differ ⇒ `PULSE_OVERLAY_RAKE_TARGET_MISMATCH` (never dropped).
- Structural zeros stay zero; an empty row cannot reach a positive target.
- Not converged ⇒ `PULSE_OVERLAY_RAKE_NOT_CONVERGED`, payload still emitted, `converged` 0.
- Bad (negative / undefined NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Rakes the table only: writing raked weights to a cohort is out of scope.

## See

- Skills: `overlay-system`, `weighting`, `op-overlay-share-of-row`.
