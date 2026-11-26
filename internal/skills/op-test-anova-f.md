---
name: op-test-anova-f
description: One-way ANOVA F-test comparing the means of a numeric field across k groups defined by split_by.
kind: operator
category: TEST
operator: TEST_ANOVA_F
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, k-sample, parametric, comparison, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

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

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — per-group Welford feeds both SS terms.
- Rejects globally, not per pair<!-- feature: TEST_TUKEY_HSD --> — follow with tier-2 `TEST_TUKEY_HSD` via `ms_within` / `df_within`<!-- /feature -->.
- Assumes equal spread; a large p from a spread test is not evidence of it.
- Normality: a normality test describes shape; it is not a gate.
- Tier-2 `one_way_from_summary` reads per-group summary rows (`n_col`, `variance_col`); same keys. A group row with a null in a used column drops (listwise, R's NA default).

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-tukey-hsd`, `op-test-anova-welch`, `op-test-kruskal-wallis`
