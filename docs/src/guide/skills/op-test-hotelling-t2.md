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

No `Response.Components`.

## Use when

Checks whether several numeric fields, taken together as one profile, differ from a target, between two groups, or before vs after.

Questions it answers:

- Do the two app versions differ on the whole set of engagement metrics at once?
- Is our average satisfaction profile across five attributes different from the benchmark profile?

Use something else:

- `TEST_T` when there is only one numeric field.

## Params

- `alpha` (0.05). `mu` — per member (default zeros). `missing` — `listwise` only.
- `variance` — `pooled` (default) or `unequal` (Krishnamoorthy–Yu); two-sample only.

Slot: one of `vector` (a `vectors` entry) / `fields` (globs ok); no `field`. `split_by` optional, categorical or `packed_bool` (levels `"0"`/`"1"`; else `PULSE_FIELD_TYPE_UNSUPPORTED`), exactly 2 levels. Paired: `vector2` / `fields2`, same count, row-wise difference; never with `split_by`.
- `weight` — both kinds; N* = Σw or n_eff (`Details` add `sum_weights`).
- `multiplicity` — `p_adjusted` ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool`: vector `coerce: binary`).

## Output

`Statistic` = T², `DF` = p. `Details`: `f`, `df1`, `df2`, `n`, `members`, per-kind means ([`multivariate-outputs`](multivariate-outputs.md)). `Variant`: `one_sample`, `paired`, `two_sample_pooled`, `two_sample_unequal`.

## Reading the output

- `statistic`: T² is the squared distance between the observed average profile and the target profile (one sample) or between the two groups' average profiles, measured in units of the fields' own spread and correlation; larger values are stronger evidence that the profiles differ; the p-value says whether it is large enough to be surprising.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Streamable. Needs n > p (n₁+n₂ > p+1 pooled); `unequal` refused on one-sample / paired; `missing: pairwise` refused.
- Singular covariance: null statistic + `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`statistical-testing`](statistical-testing.md), [`multivariate-outputs`](multivariate-outputs.md), [`op-test-t`](op-test-t.md), [`op-test-welch`](op-test-welch.md)
