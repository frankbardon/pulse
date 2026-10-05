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

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), both kinds: r is scale-free (same under either kind); t and `df` = N*−2 (Σw or Kish n_eff), Fisher CI on N*−3. `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 3 warns `PULSE_WEIGHT_LOW_NEFF`; n_eff ≤ 2 (df ≤ 0) is `PULSE_TEST_INSUFFICIENT_N`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` / `Field2` (both required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`.

## Output

`Statistic` = r (Pearson correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided via the t-statistic `r·√((n−2)/(1−r²))`. `Details.n` and `Details.r²` carry the sample size and coefficient of determination.

## Gotchas

- Streamable — extended Welford recurrence tracks the running cross-product alongside per-field moments.
- Linear-only sensitivity.
- Tier-1 (raw rows) vs tier-2 (`TEST_PEARSON_R/pearson_post` over result columns) can disagree under Simpson's paradox — pick the variant deliberately.
- Constant Field or Field2 → `PULSE_TEST_VARIANCE_ZERO`.
- Outliers dominate r; pre-clip them or switch to a rank-based correlation.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-spearman-r`, `op-test-kendall-tau`, `op-test-paired-t`
