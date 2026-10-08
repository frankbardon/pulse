```yaml
name: op-test-shapiro-wilk
description: Shapiro-Francia (W′) normality test on Field; runs per-group when SplitBy is set; p advisory outside 5..5000.
kind: operator
category: TEST
operator: TEST_SHAPIRO_WILK
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, distribution-shape, parametric, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether a numeric field looks normally distributed, overall or within each group.

Questions it answers:

- Is response time roughly bell-shaped, or clearly skewed or heavy-tailed?
- How far do scores in each group depart from a normal shape?

Use something else:

- `TEST_KS` when you compare the distributions of two groups with each other.
- `TEST_BROWN_FORSYTHE` when you want to check whether groups have equal spread.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (optional — when set, runs per-group) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = W' (Shapiro-Francia, approximates Shapiro-Wilk W; `(0, 1]`); `PValue` (small p ⇒ reject normality). With `SplitBy`: `Details.per_group` carries per-arm W and p; headline `Statistic` / `PValue` track the worst-rejecting group.

## Reading the output

- `statistic`: W' (Shapiro-Francia) measures how closely the sorted values follow the straight line expected of normal data, up to 1; values clearly below 1 point to skew, heavy tails or other departures from normal.
  - Caveat: With SplitBy, the headline W' and p-value belong to the group with the smallest p; read details.per_group for every group.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Never weighted (no standard weighted form exists): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Buffered — requires the ordered values.
- p calibrated for 5..5000 rows; outside, it still runs and `per_group[].warning` marks p advisory. Check a QQ plot.
- Not a gate: few rows miss real departures, many flag trivial ones.
- Tiny groups (`n < 3`) → `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `TEST_SHAPIRO_WILK/shapiro_francia_post` runs on a result column.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-kruskal-wallis`](op-test-kruskal-wallis.md), [`op-test-ks`](op-test-ks.md)
