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

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `variant` | enum | `mann_kendall` | The only variant. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

`field` + `order_by` (≥ 1 key) both required. **Tier-2 typical** — list it in `post_tests`; tier-1 (`tests`) works when the raw field has an ordering key.

## Inputs

`field` — numeric output column (typically a window output or a grouped aggregate). `order_by` — numeric or `date`, defining order. The test always re-sorts by `order_by`. An `order_by` naming a grouper key (`groups` or a crosstab axis) follows that grouper's key order: `day_of_week` Monday first, date-range labels in supplied order, numeric buckets by value; ISO date keys sort chronologically. Any other STRING column sorts byte-wise (alphabetically). See `op-group-date`.

## Output

`statistic` = Mann-Kendall Z (S over its SE, continuity-corrected); `p_value` two-sided normal approx. `details`: `s`, `tau` (tau-b, S / √(n₀(n₀ − n₂)) — tied value pairs n₂ leave the denominator, as R's `Kendall::MannKendall`; `null` when every value ties), `var_s` (tie-adjusted), `n`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Meaningful only over an ordered upstream series (a moving average over a date grouper is canonical); reads result rows, not raw cohort.
- Empty `order_by` → `PULSE_TEST_MISSING_ORDER_BY`. Buffered.
- A row with a null value or `order_by` key drops listwise (R's NA default); `details.n` is the n used. n < 3 -> `PULSE_TEST_INSUFFICIENT_N`; n < 8 warns (advisory; ~10+ advised).
- Seasonality-sensitive — pre-deseasonalize (a smoothing window or month grouping).

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: `statistical-testing`, `window-design`, `op-win-moving-avg`
