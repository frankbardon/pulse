```yaml
name: op-test-shapiro-wilk
description: Shapiro-Francia (W′) normality test on Field (= nortest::sf.test); runs per-group when SplitBy is set; needs n ≥ 5.
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

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (optional — when set, runs per-group) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = W' (Shapiro-Francia, approximates Shapiro-Wilk W; `(0, 1]`); `PValue` (small p ⇒ reject normality). With `SplitBy`: `Details.per_group` carries per-arm W and p; headline `Statistic` / `PValue` track the worst-rejecting group.

## Reading the output

- `statistic`: W' (Shapiro-Francia) measures how closely the sorted values follow the straight line expected of normal data, up to 1; values clearly below 1 point to skew, heavy tails or other departures from normal.
  - Caveat: With SplitBy, the headline W' and p-value belong to the group with the smallest p; read details.per_group for every group.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: a frequency-weighted W' would need the expected normal order statistics of all Σw expanded rows and Royston's p-value calibration on a tie-heavy sample, and no probability-weighted form exists); `weight: null` on the slot runs it unweighted.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — requires the ordered values.
- Matches R `nortest::sf.test` (not `shapiro.test`, a different W). p calibrated for 5..5000 rows: above 5000 `per_group[].warning` marks it advisory.
- Not a gate: few rows miss real departures, many flag trivial ones.
- Any group with `n < 5` → `PULSE_TEST_INSUFFICIENT_N`; a constant group (W′ undefined; `sf.test` NA) → `PULSE_TEST_VARIANCE_ZERO`. Either way the slot fails alone (`error`, `PULSE_SLOT_FAILED`; fatal under Strict), both tiers.
- Tier-2 variant `TEST_SHAPIRO_WILK/shapiro_francia_post` runs on a result column. A row with a null in a used column drops listwise (R's NA default); each `per_group[].n` is the n used.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-kruskal-wallis`](op-test-kruskal-wallis.md), [`op-test-ks`](op-test-ks.md)
