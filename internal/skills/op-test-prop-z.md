---
name: op-test-prop-z
description: Two-proportion z-test on the success rate of field across two split_by groups, or one-proportion against params.p0; streamable via success / total counts.
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
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `success` | string | (required) | The `field` dictionary value counted a success; on `packed_bool` `"1"` or `"0"`. |
| `p0` | float | — | In `(0, 1)`, `split_by` absent: the one-proportion test (`variant` `one_proportion`). |
| `reference` | string | — | The `split_by` level read second: `diff` reads other − reference (default: alphabetical). |
| `covariate` | string | — | A numeric pre-period field (CUPED) adjusting the success indicator; not with `p0`. |
| `interval` | enum | `wald` | `wald`, or `wilson` with `p0`. |
| `correct` | bool | `false` | `prop.test`'s continuity correction. |

Option rules, codes: `test-variant-options`.

- `weight` — p̂_g = Σw_success/Σw_g, N*_g = Σw_g (frequency) or Kish n_eff_g (probability) for the pooled rate and SEs. `successes` becomes Σw_success; `details` add `sum_weights` (+ `n_eff`) shaped like `n`.

## Inputs

`field` (required; `packed_bool` levels `"0"`/`"1"`). `split_by` (required unless `p0`, exactly 2 groups).

## Output

`statistic` = z (pooled SE under H₀); `p_value` two-sided via Φ. `details`: `groups`, `n`, `successes`, `proportion` (per arm), `diff`, `pooled`, Wald `ci_low`/`ci_high`. `effect_size.cohens_h` = 2·asin√p₁ − 2·asin√p₂. One-proportion `details`: `test-variant-options`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `success` absent, empty, not an exact dictionary entry (a shard archive's union dictionary) or, on `packed_bool`, not `"0"` / `"1"` → `PULSE_TEST_SUCCESS_VALUE_MISSING` (`details.valid_levels`).
- No `field`, or no `split_by` without `p0` → `PROCESSING_CONFIG`.
- A group's N*_g·p̂ or N*_g·(1−p̂) `< 5` (p̂ pooled or `p0`) → warns `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`.
- Over two `split_by` groups → `PULSE_TEST_SPLIT_GROUPS_LT_2`: a contingency-table question.
<!-- feature: OVERLAY_PROP_Z_CELL -->
- Crosstab cells: `OVERLAY_PROP_Z_CELL`.
<!-- /feature -->

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `statistical-testing`, `op-test-chisq`, `op-test-fisher-exact`
