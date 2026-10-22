```yaml
name: op-test-kruskal-wallis
description: Nonparametric k-group location test; ANOVA-like rank sums under tie correction.
kind: operator
category: TEST
operator: TEST_KRUSKAL_WALLIS
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, k-sample, comparison, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups.

Questions it answers:

- Do satisfaction ratings tend to differ across the four regions?
- Do skewed repair times differ across product lines?

Use something else:

- `TEST_ANOVA_WELCH` when the data are roughly normal and you want to compare averages.
- `TEST_MANN_WHITNEY_U` when there are only two groups.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |

- `weight` — frequency only: a row of weight w ranks as w identical rows — equals the test on the expanded rows. `Details` add `sum_weights` shaped like `n`; `n` / `n_total` stay raw row counts.

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = tie-corrected H; `DF` = k − 1; `PValue` via χ² survival. `Details`: `groups`, `n`, `rank_sums`, `n_total`, `tie_factor`; `effect_size.epsilon_squared` = H·(n+1)/(n²−1), omitted when every value ties.

## Reading the output

- `statistic`: H measures how far each group's average rank sits from the overall average rank, corrected for ties; larger values are stronger evidence that some groups tend to have higher values than others; the p-value says whether this H is large enough to be surprising.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — combined values ranked across all groups under tie correction.
- ≥ 50% tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Nonparametric alternative to one-way ANOVA for skewed or heavy-tailed data.
- Global only — no Dunn / Conover post-hoc; use pairwise `TEST_MANN_WHITNEY_U` with a `multiplicity` block (Holm/Bonferroni).
- Groups under ~5 rows: shaky p; only N < 2k is refused (`PULSE_TEST_INSUFFICIENT_N`). ε² unbanded.
- Tests stochastic equality, not equal medians — differing shapes can reject on shape alone.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md), [`op-test-anova-welch`](op-test-anova-welch.md)
