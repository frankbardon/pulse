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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `Field2` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `Field2` | numeric (same set) |

## Output

`Statistic` = r (Pearson correlation, `[-1, 1]`); `DF` = n − 2; `PValue` two-sided via the t-statistic `r·√((n−2)/(1−r²))`. `Details.n` and `Details.r²` carry the sample size and coefficient of determination.

## Gotchas

- Streamable — extended Welford recurrence tracks the running cross-product alongside per-field moments.
- Linear-only sensitivity; for monotonic but non-linear use `TEST_SPEARMAN_R`.
- Tier-1 (raw rows) vs tier-2 (`TEST_PEARSON_R/pearson_post` over result columns) can disagree under Simpson's paradox — pick the variant deliberately.
- Constant Field or Field2 → `PULSE_TEST_VARIANCE_ZERO`.
- Outliers dominate r; pre-clip with `FILTER_RANGE` or switch to rank-based correlation.

## See

- `pulse_examples_search tags=[correlation-analysis]`
- Skills: `statistical-testing`, `op-test-spearman-r`, `op-test-kendall-tau`, `op-test-paired-t`
