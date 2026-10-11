---
name: op-test-hotelling-t2
description: Hotelling's T² on a mean vector of p numeric members; one-sample against params.mu, or two-sample over a 2-level SplitBy with pooled covariance; exact F.
kind: operator
category: TEST
operator: TEST_HOTELLING_T2
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, two-sample, streaming-friendly]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`. `mu` — one number per member (one-sample; default zeros). `missing` — `listwise` only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (inline, globs ok); no `field`. `split_by` optional, categorical, exactly 2 levels.
- `weight` — both kinds; per-level moments on w*, N* = Σw or n_eff. `Details` add `sum_weights` (+ `n_eff`) shaped like `n`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — adds `p_adjusted` (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool` via a vector with `coerce: binary`).

## Output

`Statistic` = T², `DF` = p. `Details`: `f`, `df1`, `df2`, `n`, `n_dropped`, `members`; one-sample `mean`, `mu`; two-sample `groups` (sorted), `means`, `mean_diff`.

<!-- generated: reading-the-output -->

## Gotchas

- Streamable (one co-moment per level). Needs n > p (one-sample) / n₁+n₂ > p+1.
- Singular covariance: null statistic + `PULSE_MATRIX_SINGULAR` warning.
- `missing: pairwise` refused.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `statistical-testing`, `multivariate-design`, `op-test-t`
