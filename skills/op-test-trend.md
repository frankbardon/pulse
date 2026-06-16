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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |
| `variant` | string | `"mann_kendall"` | Algorithm name (only variant currently supported). |

Slot params: `Field` (required, numeric), `OrderBy` (required, ≥ 1 key). **Tier-2 typical** — list in `Request.PostTests`; tier-1 possible when the raw field has a natural ordering key.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric output column (typically from `WIN_MOVING_AVG` or a grouped aggregate) |
| `OrderBy` | numeric or `date` field defining the series ordering |

## Output

`Statistic` = S (Mann-Kendall score); `PValue` via the standard-normal approximation with tie correction. `Details.tau` = Kendall's τ (effect size); `Details.var_s` = adjusted variance.

## Gotchas

- Tier-2 only meaningful with an ordered upstream series — `WIN_MOVING_AVG` over a date grouper is the canonical pairing; reads result rows, never the raw cohort.
- `OrderBy` empty → `PULSE_TEST_MISSING_ORDER_BY`.
- Buffered (`Streamable=false`).
- Short series → unstable p; gate with `PULSE_TEST_INSUFFICIENT_N` (n ≥ 10).
- Sensitive to seasonality — pre-deseasonalize via `WIN_EWMA` or month grouping.

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: `statistical-testing`, `window-design`, `op-win-moving-avg`
