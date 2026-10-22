```yaml
name: op-test-anova-welch
description: Heteroscedasticity-robust one-way ANOVA with Welch-Satterthwaite df correction; streamable per-group Welford.
kind: operator
category: TEST
operator: TEST_ANOVA_WELCH
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, k-sample, comparison, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread.

Questions it answers:

- Does average response time differ across regions whose variability is very different?
- Do the store formats differ in average basket size, given some formats are far more variable?

Use something else:

- `TEST_KRUSKAL_WALLIS` when the measure is heavily skewed or only ordered.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `n_col` | string | (required) | Both required on the post-test (summary) form: result-row columns holding each group's size and sample variance. |
| `variance_col` | string | (required) | Both required on the post-test (summary) form: result-row columns holding each group's size and sample variance. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

- `weight` — per-group moments on w*, `weights` = N*_j/s²_j, df on N*_j−1 (Σw or Kish n_eff). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = Welch F*; `DF` = k−1; `PValue` via F survival. `Details`: `groups`, `n`, `group_means`, `group_variances`, `weights`, `weighted_mean`, `df_between`, `df_within` (Welch df). `effect_size.omega_squared` = df_b(F*−1)/(df_b(F*−1)+N) clamped ≥ 0 (Lakens 2013).

## Reading the output

- `statistic`: Welch's F compares the spread of the group averages with the noise inside groups, weighting each group by its own variance; larger values are stronger evidence that the averages differ by more than that noise; the p-value says whether this F is large enough to be surprising.
  - Caveat: It says whether some groups differ, not which ones.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable — same per-group Welford as classic one-way ANOVA; only the statistic + denominator change.
- Use when group spreads may differ.
- Tier-2 `welch_one_way_post` reads per-group `{mean, variance, n}`; same keys. A group row with a null in a used column drops (listwise, R's NA default).
- Post-hoc: Tukey HSD assumes equal variance; for unequal fall back to pairwise `TEST_WELCH` with a `multiplicity` block.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-brown-forsythe`](op-test-brown-forsythe.md), [`op-test-tukey-hsd`](op-test-tukey-hsd.md)
