```yaml
name: op-test-anova-f
description: One-way ANOVA F-test comparing the means of a numeric field across k groups defined by split_by.
kind: operator
category: TEST
operator: TEST_ANOVA_F
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, k-sample, parametric, comparison, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the average of a numeric measure differs across three or more groups.

Questions it answers:

- Does average spend differ across regions?
- Do the treatment arms produce different average outcomes?

Use something else:

- `TEST_ANOVA_WELCH` when the groups have clearly unequal spread.
- `TEST_KRUSKAL_WALLIS` when the measure is heavily skewed or only ordered.
- `TEST_WELCH` when there are only two groups.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `n_col` | string | (required) | Post-test (summary) form only, required there: the result column holding each group's n. |
| `variance_col` | string | (required) | Post-test form only, required there: the column holding each group's sample variance. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — SS on w*, each group on its own N*_g (Σw_g or Kish n_eff_g); `df_within` = ΣN*_g−k, may be fractional. `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < k+1 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, ≥ 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`statistic` = F; `df` = k−1; `p_value` via F survival. `details`: `groups`, `n`, `group_means`, `ss_between`, `ss_within`, `df_within`, `ms_within`; `effect_size.{eta_squared, omega_squared}` (ω² clamped ≥0).

## Reading the output

- `statistic`: F compares how far apart the group averages are with how much rows vary inside each group; larger values are stronger evidence that the groups differ by more than within-group noise; the p-value says whether this F is large enough to be surprising.
  - Caveat: F says whether some groups differ, not which ones: run TEST_TUKEY_HSD to find the pairs.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable — per-group Welford feeds both SS terms.
- Rejects globally, not per pair — follow with tier-2 `TEST_TUKEY_HSD` via `ms_within` / `df_within`.
- Assumes equal spread; a large p from a spread test is not evidence of it.
- Normality: a normality test describes shape; it is not a gate.
- Tier-2 `one_way_from_summary` reads per-group summary rows (`n_col`, `variance_col`); same keys. A group row with a null in a used column drops (listwise, R's NA default).

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-tukey-hsd`](op-test-tukey-hsd.md), [`op-test-anova-welch`](op-test-anova-welch.md), [`op-test-kruskal-wallis`](op-test-kruskal-wallis.md)
