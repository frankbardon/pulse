```yaml
name: op-test-prop-z
description: Two-proportion z-test on the success rate of Field across two SplitBy groups; streamable via success / total counts.
kind: operator
category: TEST
operator: TEST_PROP_Z
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, proportion-analysis, experiment-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the rate of one outcome, such as conversion, differs between two groups.

Questions it answers:

- Does the new checkout convert at a different rate from the old one?
- Is the share of promoters different between the two regions?

Use something else:

- `TEST_FISHER_EXACT` when some groups have only a handful of successes or failures.
- `TEST_CHISQ` when there are more than two groups or more than two outcomes.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), both kinds: p̂_g = Σw_success/Σw_g, N*_g = Σw_g (frequency) or Kish n_eff_g (probability) for the pooled rate and SEs. `successes` becomes Σw_success; `Details` add `sum_weights` (+ `n_eff`) shaped like `n` (raw rows).
- `success` — string, required. Dictionary value of Field treated as a "success".
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` (required) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `SplitBy` (required, exactly 2 groups) — same set.

## Output

`Statistic` = z (pooled SE under H₀); `PValue` two-sided via Φ. `Details`: `groups`, `n`, `successes`, `proportion` (per arm), `diff`, `pooled`, Wald `ci_low`/`ci_high`. `effect_size.cohens_h` = 2·asin√p₁ − 2·asin√p₂ (sign of `diff`).

## Reading the output

- `statistic`: z: the first group's success rate minus the second's, in standard errors computed from the pooled rate. Values far from 0 in either direction are unlikely if the true rates are equal.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.diff`: The first group's success rate minus the second's, as a proportion (0.05 = 5 percentage points).
- `details.ci_high`: Upper end of the confidence interval for details.diff.

## Gotchas

- `success` must match a dictionary value; otherwise `PULSE_TEST_INVALID_SUCCESS`.
- Streamable — per-group counts feed both numerator and pooled denominator in one pass.
- More than two `SplitBy` groups is a `(SplitBy × Field)` contingency-table question, not this test.
- Pairs with `OVERLAY_PROP_Z_CELL` for crosstab cell-level proportion tests.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-fisher-exact`](op-test-fisher-exact.md)
