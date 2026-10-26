---
name: op-win-distributed-lag
description: Distributed lag (carry-over, media adstock); a finite lag kernel (geometric, Weibull CDF or Weibull PDF) convolved over the ordered partition.
kind: operator
category: WIN
operator: WIN_DISTRIBUTED_LAG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, feature-engineering, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `kernel` | enum | (required) | `geometric` \| `weibull_cdf` \| `weibull_pdf` |
| `max_lag` | int | (required) | last lag, `[1, 10000]`; `max_lag + 1` weights |
| `decay` | float | — | geometric only, `[0, 1)`: `w_l = decay^l` |
| `shape` | float | — | Weibull only, `> 0` |
| `scale` | float | — | Weibull only, `> 0` |
| `normalize` | bool | `true` | weights divided by their sum |

Weibull kernels: `weibull_cdf` `w_l = exp(−(l/scale)^shape)` (survival, `w_0 = 1`); `weibull_pdf` `w_l = f(l + 1)` (density one lag on). `partition_by`, `order_by` (≥1), `field` (numeric) required; `frame` refused.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

`y_i = Σ_l w_l · x_{i−l}`, one `float64` per row in `Label`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Lags before the partition start or on a null row add 0 (weights not renormalised); a null row emits `null`.
- Starts from zero, unlike<!-- feature: WIN_EWMA --> `WIN_EWMA`<!-- /feature -->; normalised geometric ≈ EWMA (`alpha = 1 − decay`) once `decay^i` is negligible.
- Missing/unknown `kernel`, bad `max_lag`, `decay`, `shape`, `scale`, a param the kernel does not read → `PULSE_WINDOW_INVALID` (predict and runtime).
- Not weightable; forces buffered execution.
- Regressions, post-window formulas, aggregations and groupers may read the label (`regression-model-options`, `windows-first`).

## See

- `pulse_examples_search tags=[time-series, feature-engineering]`
- Skills: `op-win-ewma`, `window-design`, `expression-language`
