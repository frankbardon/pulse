---
name: op-test-pearson-r
description: Parametric Pearson correlation test between two numeric fields; streamable via online cross-product.
kind: operator
category: TEST
operator: TEST_PEARSON_R
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, correlation-analysis, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

- `weight` — r is scale-free (same under either kind); t and `df` = N*−2 (Σw or Kish n_eff), Fisher CI on N*−3. `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 3 warns `PULSE_WEIGHT_LOW_NEFF`; n_eff ≤ 2 (df ≤ 0) is `PULSE_TEST_INSUFFICIENT_N`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `field2` (required) — the same set.

## Output

`statistic` = r (Pearson correlation, `[-1, 1]`); `df` = n − 2; `p_value` two-sided via the t-statistic `r·√((n−2)/(1−r²))`. `details`: `n`, `t`, Fisher-z `ci_low` / `ci_high` on r, `covariance`, `mean_x` / `mean_y`, `variance_x` / `variance_y`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable — extended Welford recurrence tracks the running cross-product alongside per-field moments.
- Linear-only sensitivity.
- Tier-1 (raw rows) vs tier-2 (`TEST_PEARSON_R/pearson_post` over result columns) can disagree under Simpson's paradox — pick the variant deliberately. A row with a null in a used column drops listwise (R's NA default); `details.n` is the n used.<!-- feature: WIN_LAG --> A `WIN_LAG` output's leading null needs no `default` (`window-design`).<!-- /feature -->
- Constant `field` or `field2` → `PULSE_TEST_VARIANCE_ZERO`.
- Outliers dominate r; pre-clip them or switch to a rank-based correlation.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-spearman-r`, `op-test-kendall-tau`, `op-test-paired-t`
