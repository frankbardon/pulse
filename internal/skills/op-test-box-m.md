---
name: op-test-box-m
description: Box's M test of one shared covariance across a SplitBy of 2+ levels; chi-square headline, F approximation in details. The MANOVA / Hotelling assumption check.
kind: operator
category: TEST
operator: TEST_BOX_M
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, homogeneity-test, streaming-friendly]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`. `missing` — `listwise` only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`; no `field`, `vector2` / `fields2`. `split_by` required, categorical, ≥ 2 levels.
- `weight` — both kinds; per-level ν_g = N*_g − 1.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = Box's χ², `DF` = p(p + 1)/2·(k − 1), `PValue`. `Details`: `m`, `f`, `df1`, `df2`, `f_p_value` (F form), `members`, `groups`, `n`, `n_dropped`, `log_det` (per level), `log_det_pooled`.

<!-- generated: reading-the-output -->

## Gotchas

- Streamable. Levels need n ≥ 2; n ≤ p in a level is singular.
- Singular covariance: null figures + `PULSE_MATRIX_SINGULAR` warning.
- Very sensitive to non-normality; many read only p < 0.001.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `multivariate-design`, `op-test-manova`, `op-test-hotelling-t2`
