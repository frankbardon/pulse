```yaml
name: op-test-box-m
description: Box's M test of one shared covariance across a SplitBy of 2+ levels; chi-square headline, F approximation in details. The MANOVA / Hotelling assumption check.
kind: operator
category: TEST
operator: TEST_BOX_M
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, homogeneity-test, streaming-friendly]
```

Tests emit statistic / p-value; no `Response.Components`.

## Use when

Checks whether several groups share one spread pattern across a set of numeric fields, the assumption behind comparing average profiles.

Questions it answers:

- Before comparing segment profiles, do the segments have similar spread across the metrics?
- Is it safe to pool the two groups' covariance for a multivariate comparison?

Use something else:

- `TEST_BROWN_FORSYTHE` when there is only one numeric field.

## Params

- `alpha` — float, default `0.05`. `missing` — `listwise` only.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`, `vector2` / `fields2`. `split_by` required, categorical or `packed_bool` (levels `"0"`/`"1"`), ≥ 2 levels.
- `weight` — both kinds; per-level ν_g = N*_g − 1.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = Box's χ², `DF` = p(p + 1)/2·(k − 1), `PValue`. `Details`: `m`, `f`, `df1`, `df2`, `f_p_value` (F form), `members`, `groups`, `n`, `n_dropped`, `log_det` (per level), `log_det_pooled`.

## Reading the output

- `statistic`: A chi-square that grows as the groups' spreads and correlations across the fields differ; a small p-value says the groups do not share one covariance.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.m`: Box's M, the raw gap between the pooled and the per-group covariances before the small-sample correction; 0 when every group's covariance is the same.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable. Levels need n ≥ 2 (Σw under `frequency`); n ≤ p in a level is singular.
- Singular covariance: null figures + `PULSE_MATRIX_SINGULAR` warning.
- Very sensitive to non-normality; many read only p < 0.001.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-test-manova`](op-test-manova.md), [`op-test-hotelling-t2`](op-test-hotelling-t2.md)
