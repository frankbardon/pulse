```yaml
name: op-test-prop-z
description: Two-proportion z-test on the success rate of Field across two SplitBy groups, or one-proportion against params.p0; streamable via success / total counts.
kind: operator
category: TEST
operator: TEST_PROP_Z
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, proportion-analysis, experiment-analysis, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the rate of one outcome, such as conversion, differs between two groups, or from a planned rate.

Questions it answers:

- Does the new checkout convert at a different rate from the old one?
- Is the share of promoters different between the two regions?

Use something else:

- `TEST_FISHER_EXACT` when some groups have only a handful of successes or failures.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), both kinds: p̂_g = Σw_success/Σw_g, N*_g = Σw_g (frequency) or Kish n_eff_g (probability) for the pooled rate and SEs. `successes` becomes Σw_success; `Details` add `sum_weights` (+ `n_eff`) shaped like `n` (raw rows).
- `success` — string, required. Dictionary value of Field treated as a "success"; on a `packed_bool` Field strictly `"1"` or `"0"`.
- `reference` — string, two-proportion: the `SplitBy` level read second, so `diff`, CI, z and `cohens_h` read other − reference (default: alphabetical, second level). Not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`).
- `p0` — float in `(0, 1)`, optional: with `SplitBy` absent, the one-proportion test (`Variant` `one_proportion`). Out of range or beside `SplitBy` → `PULSE_TEST_P0_INVALID` (predict too).
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` (required) — categorical: `categorical_u8`/`u16`/`u32`, or `packed_bool` (levels `"0"`/`"1"`). `SplitBy` (required unless `p0`, exactly 2 groups) — same set. Any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime (field first).

## Output

`Statistic` = z (pooled SE under H₀); `PValue` two-sided via Φ. `Details`: `groups`, `n`, `successes`, `proportion` (per arm), `diff`, `pooled`, Wald `ci_low`/`ci_high`. `effect_size.cohens_h` = 2·asin√p₁ − 2·asin√p₂ (sign of `diff`). One-proportion: z = (p̂ − p0)/√(p0(1−p0)/N*); scalar `n`, `successes`, `proportion`, plus `p0`, `diff` = p̂ − p0, Wald `ci_low`/`ci_high` on `diff`, `cohens_h` vs p0.

## Reading the output

- `statistic`: z: the first group's success rate minus the second's, in standard errors computed from the pooled rate. Values far from 0 in either direction are unlikely if the true rates are equal. With params.p0 (Variant one_proportion) it is the observed rate minus p0, in standard errors computed from p0.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.ci_high`: Upper end of the confidence interval for details.diff.

## Gotchas

- `success` absent or empty → `PULSE_TEST_SUCCESS_VALUE_MISSING`; on a `packed_bool` outcome anything but `"0"` / `"1"` (`true`, `yes`) is the same code with `details.valid_levels`. A categorical `success` is NOT checked against the dictionary: a misspelt level counts zero successes — check `successes`.
- No `field`, or no `split_by` without `p0` → `PROCESSING_CONFIG` (predict too).
- Streamable — per-group counts feed both numerator and pooled denominator in one pass.
- More than two `SplitBy` groups is a `(SplitBy × Field)` contingency-table question, not this test.
- Pairs with `OVERLAY_PROP_Z_CELL` for crosstab cell-level proportion tests.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-fisher-exact`](op-test-fisher-exact.md)
