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

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `variant` | enum | `mann_kendall` | The only variant. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

`field` + `order_by` (≥ 1 key) both required. **Tier-2 typical** — list it in `post_tests`; tier-1 (`tests`) works when the raw field has an ordering key.

## Inputs

`field` — numeric output column (typically a window output or a grouped aggregate). `order_by` — numeric or `date`, defining order. The test always re-sorts by `order_by`. An `order_by` naming a grouper key (`groups` or a crosstab axis) follows that grouper's key order: `day_of_week` Monday first, date-range labels in supplied order, numeric buckets by value; ISO date keys sort chronologically. Any other STRING column sorts byte-wise (alphabetically). See [`op-group-date`](op-group-date.md).

## Output

`statistic` = Mann-Kendall Z (S over its SE, continuity-corrected); `p_value` two-sided normal approx. `details`: `s`, `tau` (tau-b, S / √(n₀(n₀ − n₂)) — tied value pairs n₂ leave the denominator, as R's `Kendall::MannKendall`; `null` when every value ties), `var_s` (tie-adjusted), `n`.

## Reading the output

- `statistic`: The Mann-Kendall Z score: the trend count S (details.s) in standard errors, with a continuity correction. Values far from 0 point to a consistent tendency to rise or fall (not necessarily at an even rate).
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.s`: S counts every pair of points: +1 when the later point is higher, -1 when it is lower, 0 when tied.

## Contract

- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: it runs Mann-Kendall over the aggregated result rows, which carry no row weights); `weight: null` on the slot runs it unweighted.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Meaningful only over an ordered upstream series (a moving average over a date grouper is canonical); reads result rows, not raw cohort.
- Empty `order_by` → `PULSE_TEST_MISSING_ORDER_BY`. Buffered.
- A row with a null value or `order_by` key drops listwise (R's NA default); `details.n` is the n used. n < 3 -> `PULSE_TEST_INSUFFICIENT_N`; n < 8 warns (advisory; ~10+ advised).
- Seasonality-sensitive — pre-deseasonalize (a smoothing window or month grouping).

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: [`statistical-testing`](statistical-testing.md), [`window-design`](window-design.md), [`op-win-moving-avg`](op-win-moving-avg.md)
