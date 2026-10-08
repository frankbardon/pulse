```yaml
name: op-test-ks
description: Kolmogorov-Smirnov two-sample distribution test on Field partitioned by SplitBy.
kind: operator
category: TEST
operator: TEST_KS
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, distribution-shape, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape.

Questions it answers:

- Do order values in the two regions have the same overall distribution?
- Has the shape of response times changed between the old and new system?

Use something else:

- `TEST_SHAPIRO_WILK` when you want to check one field against the normal distribution.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`. Always two-sided; there is no one-sided form.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): ECDFs step by Σw, p on the Σw sizes — equals the expanded rows. `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).

- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = D (sup |F₁(x) − F₂(x)|); `PValue` two-sided via Smirnov asymptotic distribution. `Details.groups` + per-arm `Details.n`.

## Reading the output

- `statistic`: D is the largest vertical gap between the two groups' cumulative distributions, from 0 (identical) to 1 (no overlap at all).
  - Caveat: D is most sensitive to differences near the middle of the distributions and less to differences in the tails.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Buffered — both ECDFs must materialize and sort before comparison.
- Sensitive to distribution shape, not just mean.
- Small-n approximation drifts; only n < 2 per group is refused (`PULSE_TEST_INSUFFICIENT_N`).
- Tier-2 variant `TEST_KS/two_sample_post` runs between two output columns of the result set.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md), [`op-test-shapiro-wilk`](op-test-shapiro-wilk.md)
