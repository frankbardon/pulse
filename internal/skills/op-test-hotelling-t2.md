---
name: op-test-hotelling-t2
description: Hotelling's T² on a mean vector of p numeric members; one-sample or paired (difference vector) against params.mu, or two-sample over a 2-level SplitBy with pooled or unequal covariance.
kind: operator
category: TEST
operator: TEST_HOTELLING_T2
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, two-sample, paired, streaming-friendly]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`. `mu` — one number per member (one-sample / paired; default zeros). `missing` — `listwise` only.
- `variance` — `pooled` (default, exact F) or `unequal` (Krishnamoorthy–Yu, fractional `df2`); two-sample only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (inline, globs ok); no `field`. `split_by` optional, categorical, exactly 2 levels. Paired: `vector2` / `fields2`, same count, row-wise `members − members2`; never with `split_by`.
- `weight` — both kinds; per-level moments on w*, N* = Σw or n_eff. `Details` add `sum_weights` (+ `n_eff`) shaped like `n`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — adds `p_adjusted` (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool` via a vector with `coerce: binary`).

## Output

`Statistic` = T², `DF` = p. `Details`: `f`, `df1`, `df2`, `n`, `n_dropped`, `members`; one-sample `mean`, `mu`; paired `members2`, `mean_diff`, `mu`; two-sample `groups` (sorted), `means`, `mean_diff`. `Variant`: `one_sample` / `paired` / `two_sample_pooled` / `two_sample_unequal`.

<!-- generated: reading-the-output -->

## Gotchas

- Streamable (one co-moment per level). Needs n > p (one-sample, paired) / n₁+n₂ > p+1 (pooled) / n > p per level (unequal).
- `unequal` at p = 1 is Welch's t; refused on one-sample / paired.
- Singular covariance: null statistic + `PULSE_MATRIX_SINGULAR` warning.
- `missing: pairwise` refused.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `statistical-testing`, `multivariate-design`, `op-test-t`, `op-test-paired-t`, `op-test-welch`
