```yaml
name: op-overlay-rake
kind: operator
category: OVERLAY
operator: OVERLAY_RAKE
description: Rake a crosstab to target row and column margins (IPF) — fitted table, adjustment factors and a convergence report; the host is never changed.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Rake a crosstab to known row and column totals: the fitted table, per-cell adjustment factors and whether the fit converged.

Questions it answers:

- What would this age by region table look like if its margins matched the census shares?
- By how much does each cell have to be scaled up or down to hit the population totals?

Use something else:

- `OVERLAY_CHISQ_MATRIX` when you want to test whether rows and columns are associated.

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

| Name | Type | Default | Description |
|---|---|---|---|
| `row_targets` | object | (required) | `{key: value}`, one per host key, no extras. |
| `col_targets` | object | (required) | `{key: value}`, one per host key, no extras. |
| `targets_as` | `counts` \| `shares` | `shares` | `shares` (default; normalized, scaled to the host total) \| `counts` (totals must agree). |
| `max_iter` | int | `100` | Iteration cap. |
| `tolerance` | float | 1e-08 | Convergence threshold on the maximum relative margin error. |


## Host shape

MATRIX crosstab of count or sum cells.

## Output

Primary MATRIX: fitted table. Part `factors` (MATRIX fitted / observed; `null` where observed is 0). Summary params: `iterations`, `residual`, `converged`.

## Reading the output

- `cells.value`: The raked count for the cell: the table rescaled so its row and column totals match the targets while keeping its internal pattern.
- `parts.factors.cells.value`: How much the cell was scaled: the raked count over the observed one (above 1 = scaled up). null where the observed cell was 0, which stays 0.
- `summary.parameters.converged`: 1 when every fitted margin is within the tolerance of its target, 0 when the fit stopped at max_iter first (the table still ships, with a warning).

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- Key mismatch, or `counts` totals differ ⇒ `PULSE_OVERLAY_RAKE_TARGET_MISMATCH` (never dropped).
- Structural zeros stay zero; an empty row cannot reach a positive target.
- Not converged ⇒ `PULSE_OVERLAY_RAKE_NOT_CONVERGED`, payload still emitted, `converged` 0.
- Bad (negative / undefined NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Rakes the table only: writing raked weights to a cohort is out of scope.

## See

- Skills: [`overlay-system`](overlay-system.md), [`weighting`](weighting.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md).
