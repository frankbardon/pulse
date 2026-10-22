```yaml
name: op-test-pearson-r
description: Parametric Pearson correlation test between two numeric fields; streamable via online cross-product.
kind: operator
category: TEST
operator: TEST_PEARSON_R
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, correlation-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Measures how strongly two numeric fields rise and fall together along a straight line.

Questions it answers:

- Do customers who spend more also visit more often?
- Does study time go with higher test scores?

Use something else:

- `TEST_SPEARMAN_R` when the link is consistent but curved, or the values are ranks.
- `TEST_KENDALL_TAU` when the sample is small or has many tied values.
- `TEST_CHISQ` when both fields are categories.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

- `weight` — r is scale-free (same under either kind); t and `df` = N*−2 (Σw or Kish n_eff), Fisher CI on N*−3. `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 3 warns `PULSE_WEIGHT_LOW_NEFF`; n_eff ≤ 2 (df ≤ 0) is `PULSE_TEST_INSUFFICIENT_N`.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `Field2` (required) — the same set.

## Output

`Statistic` = r (Pearson correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided via the t-statistic `r·√((n−2)/(1−r²))`. `Details.n` and `Details.r²` carry the sample size and coefficient of determination.

## Reading the output

- `statistic`: r measures how closely the two fields follow a straight line together, from -1 to +1; 0 means no straight-line link.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.ci_low`: Lower end of the confidence interval for r at the 1 - alpha level (95% by default).
- `details.ci_high`: Upper end of the confidence interval for r at the 1 - alpha level (95% by default).

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`field2`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD2_NOT_NUMERIC`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable — extended Welford recurrence tracks the running cross-product alongside per-field moments.
- Linear-only sensitivity.
- Tier-1 (raw rows) vs tier-2 (`TEST_PEARSON_R/pearson_post` over result columns) can disagree under Simpson's paradox — pick the variant deliberately. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used. A `WIN_LAG` column's leading null needs no `default` ([`window-design`](window-design.md)).
- Constant Field or Field2 → `PULSE_TEST_VARIANCE_ZERO`.
- Outliers dominate r; pre-clip them or switch to a rank-based correlation.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-spearman-r`](op-test-spearman-r.md), [`op-test-kendall-tau`](op-test-kendall-tau.md), [`op-test-paired-t`](op-test-paired-t.md)
