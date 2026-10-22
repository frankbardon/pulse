---
name: op-win-ewma
description: Exponentially weighted moving average; s_i = alpha*x_i + (1-alpha)*s_{i-1}.
kind: operator
category: WIN
operator: WIN_EWMA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | — | Smoothing factor in `(0, 1]`; higher = more weight on recent. |
| `decay` | float | — | Carry-over share in `[0, 1)` (adstock decay); `alpha = 1 − decay`. |

Give EXACTLY ONE of `alpha` / `decay` (manifest `exactly_one_of: smoothing`; a Recommend draft names `alpha`).

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (REQUIRED `{mode: "rows"}`; ignored by the recurrence — manifest `ignores_frame`). `field` (required, numeric).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One `float64` per row written to `Label` (default `WIN_EWMA_<field>`). Recurrence seeds from the first non-null value in the partition.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Exactly one of `alpha` / `decay` — both, neither, `alpha` outside `(0, 1]` or `decay` outside `[0, 1)` → `PULSE_WINDOW_INVALID` (predict and runtime alike).
- `alpha = 1 − decay` from the decay's EXACT decimal: `decay: 0.7` smooths bit-identically to `alpha: 0.3` (not float `1 − 0.7`). `decay: 0` is `alpha: 1` (identity).
- Rows preceding the first non-null emit `null` (no seed).
- Null values emit `null` but state survives through to the next non-null row.
- Result rows are NOT reordered — use `Request.Sort`.
- For media adstock use<!-- feature: WIN_ADSTOCK --> `WIN_ADSTOCK`<!-- /feature -->: carry-over starts from zero, with geometric or Weibull lag kernels.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-moving-avg`, `op-win-running-avg`, `op-win-adstock`
