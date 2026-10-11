```yaml
name: op-test-hotelling-t2
description: Hotelling's T² on a mean vector of p numeric members; one-sample or paired (difference vector) against params.mu, or two-sample over a 2-level SplitBy with pooled or unequal covariance.
kind: operator
category: TEST
operator: TEST_HOTELLING_T2
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, two-sample, paired, streaming-friendly]
```

Tests emit statistic / p-value; no `Response.Components`.

## Use when

Checks whether several numeric fields, taken together as one profile, differ from a target, between two groups, or before vs after.

Questions it answers:

- Do the two app versions differ on the whole set of engagement metrics at once?
- Is our average satisfaction profile across five attributes different from the benchmark profile?

Use something else:

- `TEST_T` when there is only one numeric field.

## Params

- `alpha` — float, default `0.05`. `mu` — one number per member (one-sample / paired; default zeros). `missing` — `listwise` only.
- `variance` — `pooled` (default, exact F) or `unequal` (Krishnamoorthy–Yu, fractional `df2`); two-sample only.

Slot: exactly one of `vector` (a `vectors` entry) / `fields` (inline, globs ok); no `field`. `split_by` optional, categorical, exactly 2 levels. Paired: `vector2` / `fields2`, same count, row-wise `members − members2`; never with `split_by`.
- `weight` — both kinds; per-level moments on w*, N* = Σw or n_eff. `Details` add `sum_weights` (+ `n_eff`) shaped like `n`.
- `multiplicity` — adds `p_adjusted` ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool` via a vector with `coerce: binary`).

## Output

`Statistic` = T², `DF` = p. `Details`: `f`, `df1`, `df2`, `n`, `n_dropped`, `members`; one-sample `mean`, `mu`; paired `members2`, `mean_diff`, `mu`; two-sample `groups` (sorted), `means`, `mean_diff`. `Variant`: `one_sample` / `paired` / `two_sample_pooled` / `two_sample_unequal`.

## Reading the output

- `statistic`: T² is the squared distance between the observed average profile and the target profile (one sample) or between the two groups' average profiles, measured in units of the fields' own spread and correlation; larger values are stronger evidence that the profiles differ; the p-value says whether it is large enough to be surprising.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Streamable (one co-moment per level). Needs n > p (one-sample, paired) / n₁+n₂ > p+1 (pooled) / n > p per level (unequal).
- `unequal` at p = 1 is Welch's t; refused on one-sample / paired.
- Singular covariance: null statistic + `PULSE_MATRIX_SINGULAR` warning.
- `missing: pairwise` refused.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`statistical-testing`](statistical-testing.md), [`multivariate-design`](multivariate-design.md), [`op-test-t`](op-test-t.md), [`op-test-paired-t`](op-test-paired-t.md), [`op-test-welch`](op-test-welch.md)
