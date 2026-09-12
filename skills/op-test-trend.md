---
name: op-test-trend
description: Mann-Kendall trend test over an ordered numeric series; tier-2 over windowed result rows.
kind: operator
category: TEST
operator: TEST_TREND
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-2-test, nonparametric, trend-detection, time-series, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `variant` — string, default `"mann_kendall"` (the only variant).

`Field` + `OrderBy` (≥ 1 key) both required. **Tier-2 typical** — list in `Request.PostTests`; tier-1 works when the raw field has a natural ordering key.

## Inputs

`Field` — numeric output column (typically `WIN_MOVING_AVG` or a grouped aggregate). `OrderBy` — numeric or `date`, defining series order.

## Output

`Statistic` = S (Mann-Kendall score); `PValue` via standard-normal approximation with tie correction. `Details.tau` = Kendall's τ; `Details.var_s` = adjusted variance.

## Gotchas

- Meaningful only over an ordered upstream series — `WIN_MOVING_AVG` over a date grouper is canonical; reads result rows, never the raw cohort.
- Empty `OrderBy` → `PULSE_TEST_MISSING_ORDER_BY`. Buffered (`Streamable=false`).
- Short series → unstable p; gated by `PULSE_TEST_INSUFFICIENT_N` (n ≥ 10).
- Seasonality-sensitive — pre-deseasonalize via `WIN_EWMA` or month grouping.

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: `statistical-testing`, `window-design`, `op-win-moving-avg`
