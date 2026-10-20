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

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `variant` — string, default `"mann_kendall"` (the only variant).

`Field` + `OrderBy` (≥ 1 key) both required. **Tier-2 typical** — list in `Request.PostTests`; tier-1 works when the raw field has an ordering key.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` — numeric output column (typically a smoothed window column or a grouped aggregate). `OrderBy` — numeric or `date`, defining order. The test always re-sorts by `OrderBy`. An `OrderBy` naming a grouper key column (`groups` or a crosstab axis) follows that grouper's key order: `day_of_week` Monday first, date-range labels in supplied order, numeric buckets by value; ISO date keys sort chronologically. Any other STRING column sorts byte-wise (alphabetically). See `op-group-date`.

## Output

`Statistic` = Mann-Kendall Z (S over its SE, continuity-corrected); `PValue` two-sided normal approx. `Details`: `s`, `tau` (tau-b, S / √(n₀(n₀ − n₂)) — tied value pairs n₂ leave the denominator, as R's `Kendall::MannKendall`; `null` when every value ties), `var_s` (tie-adjusted), `n`.

<!-- generated: reading-the-output -->

## Gotchas

- Never weighted (it reads aggregated result rows, which carry no row weights): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Meaningful only over an ordered upstream series (a moving average over a date grouper is canonical); reads result rows, not raw cohort.
- Empty `OrderBy` → `PULSE_TEST_MISSING_ORDER_BY`. Buffered.
- A row with a null value or `OrderBy` key drops listwise (R's NA default); `details.n` is the n used. n < 3 -> `PULSE_TEST_INSUFFICIENT_N`; n < 8 warns (advisory; ~10+ advised).
- Seasonality-sensitive — pre-deseasonalize (a smoothing window or month grouping).

## See

- `pulse_examples_search tags=[trend-detection]`
- Skills: `statistical-testing`, `window-design`, `op-win-moving-avg`
