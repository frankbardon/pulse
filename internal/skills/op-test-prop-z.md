---
name: op-test-prop-z
description: Two-proportion z-test on the success rate of Field across two SplitBy groups, or one-proportion against params.p0; streamable via success / total counts.
kind: operator
category: TEST
operator: TEST_PROP_Z
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, proportion-analysis, experiment-analysis, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `RejectNull` when p < alpha. |
| `success` | string | (required) | The `Field` dictionary value counted a success; on `packed_bool` strictly `"1"` or `"0"`. |
| `p0` | float | — | In `(0, 1)`, `SplitBy` absent: the one-proportion test (`Variant` `one_proportion`). Detail: `test-variant-options`. |
| `reference` | string | — | The `SplitBy` level read second: `diff` reads other − reference (default: alphabetical). Codes: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period column (CUPED) adjusting the success indicator; not with `p0`. Detail: `test-variant-options`. |

- `weight` — p̂_g = Σw_success/Σw_g, N*_g = Σw_g (frequency) or Kish n_eff_g (probability) for the pooled rate and SEs. `successes` becomes Σw_success; `Details` add `sum_weights` (+ `n_eff`) shaped like `n` (raw rows).

## Inputs

`Field` (required; `packed_bool` levels `"0"`/`"1"`). `SplitBy` (required unless `p0`, exactly 2 groups).

## Output

`Statistic` = z (pooled SE under H₀); `PValue` two-sided via Φ. `Details`: `groups`, `n`, `successes`, `proportion` (per arm), `diff`, `pooled`, Wald `ci_low`/`ci_high`. `effect_size.cohens_h` = 2·asin√p₁ − 2·asin√p₂ (sign of `diff`). One-proportion `Details`: `test-variant-options`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `success` absent, empty, not an exact dictionary entry (a shard archive's union dictionary) or, on `packed_bool`, anything but `"0"` / `"1"` → `PULSE_TEST_SUCCESS_VALUE_MISSING` (`details.valid_levels`), never a silent zero-success run; `pulse inspect` lists the entries.
- No `field`, or no `split_by` without `p0` → `PROCESSING_CONFIG` (predict too).
- More than two `SplitBy` groups is a `(SplitBy × Field)` contingency-table question, not this test.
<!-- feature: OVERLAY_PROP_Z_CELL -->
- Pairs with `OVERLAY_PROP_Z_CELL` for crosstab cell-level proportion tests.
<!-- /feature -->

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `statistical-testing`, `op-test-chisq`, `op-test-fisher-exact`
