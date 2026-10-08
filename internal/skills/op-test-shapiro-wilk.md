---
name: op-test-shapiro-wilk
description: Shapiro-Francia (W′) normality test on Field; runs per-group when SplitBy is set; p advisory outside 5..5000.
kind: operator
category: TEST
operator: TEST_SHAPIRO_WILK
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, distribution-shape, parametric, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (optional — when set, runs per-group) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = W' (Shapiro-Francia, approximates Shapiro-Wilk W; `(0, 1]`); `PValue` (small p ⇒ reject normality). With `SplitBy`: `Details.per_group` carries per-arm W and p; headline `Statistic` / `PValue` track the worst-rejecting group.

<!-- generated: reading-the-output -->

## Gotchas

- Never weighted (no standard weighted form exists): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Buffered — requires the ordered values.
- p calibrated for 5..5000 rows; outside, it still runs and `per_group[].warning` marks p advisory. Check a QQ plot.
- Not a gate: few rows miss real departures, many flag trivial ones.
- Tiny groups (`n < 3`) → `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `TEST_SHAPIRO_WILK/shapiro_francia_post` runs on a result column.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-kruskal-wallis`, `op-test-ks`
