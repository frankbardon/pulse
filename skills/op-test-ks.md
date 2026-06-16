---
name: op-test-ks
description: Kolmogorov-Smirnov two-sample distribution test on Field partitioned by SplitBy.
kind: operator
category: TEST
operator: TEST_KS
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, distribution-shape, buffered-pipeline]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |
| `alternative` | enum | `"two-sided"` | `"two-sided"` / `"less"` / `"greater"`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`Statistic` = D (sup |F₁(x) − F₂(x)|); `PValue` via Smirnov asymptotic distribution. `Details` carries per-arm n + the alternative.

## Gotchas

- Buffered — both ECDFs must materialize and sort before comparison.
- Sensitive to distribution shape, not just mean; complementary to `TEST_MANN_WHITNEY_U` (location).
- Small-n approximation drifts; gate with `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `TEST_KS/two_sample_post` runs between two output columns of the result set.
- Pairs with `OVERLAY_KS_VS_POP` for facet-vs-population distribution drift.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `statistical-testing`, `op-test-mann-whitney-u`, `op-test-shapiro-wilk`
