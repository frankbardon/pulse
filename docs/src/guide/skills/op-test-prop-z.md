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

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `RejectNull` when p < alpha. |
| `success` | string | (required) | The `Field` dictionary value counted a success; on `packed_bool` strictly `"1"` or `"0"`. |
| `p0` | float | — | In `(0, 1)`, `SplitBy` absent: the one-proportion test (`Variant` `one_proportion`). Detail: [`test-variant-options`](test-variant-options.md). |
| `reference` | string | — | The `SplitBy` level read second: `diff` reads other − reference (default: alphabetical). Codes: [`test-variant-options`](test-variant-options.md). |
| `covariate` | string | — | A numeric pre-period column (CUPED) adjusting the success indicator; not with `p0`. Detail: [`test-variant-options`](test-variant-options.md). |

- `weight` — p̂_g = Σw_success/Σw_g, N*_g = Σw_g (frequency) or Kish n_eff_g (probability) for the pooled rate and SEs. `successes` becomes Σw_success; `Details` add `sum_weights` (+ `n_eff`) shaped like `n` (raw rows).

## Inputs

`Field` (required; `packed_bool` levels `"0"`/`"1"`). `SplitBy` (required unless `p0`, exactly 2 groups).

## Output

`Statistic` = z (pooled SE under H₀); `PValue` two-sided via Φ. `Details`: `groups`, `n`, `successes`, `proportion` (per arm), `diff`, `pooled`, Wald `ci_low`/`ci_high`. `effect_size.cohens_h` = 2·asin√p₁ − 2·asin√p₂ (sign of `diff`). One-proportion `Details`: [`test-variant-options`](test-variant-options.md).

## Reading the output

- `statistic`: z: the first group's success rate minus the second's, in standard errors computed from the pooled rate. Values far from 0 in either direction are unlikely if the true rates are equal. With params.p0 (Variant one_proportion) it is the observed rate minus p0, in standard errors computed from p0.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.ci_high`: Upper end of the confidence interval for details.diff.

## Contract

- Types (`field`, `split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- `success` absent, empty, not an exact dictionary entry (a shard archive's union dictionary) or, on `packed_bool`, anything but `"0"` / `"1"` → `PULSE_TEST_SUCCESS_VALUE_MISSING` (`details.valid_levels`), never a silent zero-success run; `pulse inspect` lists the entries.
- No `field`, or no `split_by` without `p0` → `PROCESSING_CONFIG` (predict too).
- More than two `SplitBy` groups is a `(SplitBy × Field)` contingency-table question, not this test.
- Pairs with `OVERLAY_PROP_Z_CELL` for crosstab cell-level proportion tests.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-fisher-exact`](op-test-fisher-exact.md)
