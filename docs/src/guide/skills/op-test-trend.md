```yaml
name: op-test-trend
description: Mann-Kendall trend test over an ordered numeric series; tier-2 over windowed result rows.
kind: operator
category: TEST
operator: TEST_TREND
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-2-test, nonparametric, trend-detection, time-series, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling.

Questions it answers:

- Is monthly churn creeping up?
- Has weekly average wait time been falling over the year?

Use something else:

- `REG_OLS` when you want the size of a straight-line slope.
- `TEST_WELCH` when you compare the averages of two specific periods.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `variant` — string, default `"mann_kendall"` (the only variant).

`Field` + `OrderBy` (≥ 1 key) both required. **Tier-2 typical** — list in `Request.PostTests`; tier-1 works when the raw field has an ordering key.

- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` — numeric output column (typically a smoothed window column or a grouped aggregate). `OrderBy` — numeric or `date`, defining order.

## Output

`Statistic` = Mann-Kendall Z (S over its SE, continuity-corrected); `PValue` two-sided normal approx. `Details`: `s`, `tau`, `var_s` (tie-adjusted), `n`.

## Reading the output

- `statistic`: The Mann-Kendall Z score: the trend count S (details.s) in standard errors, with a continuity correction. Values far from 0 point to a consistent tendency to rise or fall (not necessarily at an even rate).
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.s`: S counts every pair of points: +1 when the later point is higher, -1 when it is lower, 0 when tied.

## Gotchas

- Never weighted (it reads aggregated result rows, which carry no row weights): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Meaningful only over an ordered upstream series (a moving average over a date grouper is canonical); reads result rows, not raw cohort.
- Empty `OrderBy` → `PULSE_TEST_MISSING_ORDER_BY`. Buffered.
- n < 3 -> `PULSE_TEST_INSUFFICIENT_N`; n < 8 warns (advisory; ~10+ advised).
- Seasonality-sensitive — pre-deseasonalize (a smoothing window or month grouping).

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: [`statistical-testing`](statistical-testing.md), [`window-design`](window-design.md), [`op-win-moving-avg`](op-win-moving-avg.md)
