---
name: op-test-t
description: One-sample or two-sample Welch t-test on a numeric field; split_by switches to the two-sample variant.
kind: operator
category: TEST
operator: TEST_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, t-test, tier-1-test, parametric, two-sample, one-sample, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `mu` | float | `0` | Hypothesized mean, one-sample only; ignored when `split_by` is set. |
| `reference` | string | — | The baseline `split_by` level, by value: `diff` = other − reference (default: the alphabetically second level, so `diff` = first − second). Codes: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period field (CUPED): the test runs on `y − θ(x − x̄)`; needs `split_by`. Detail: `test-variant-options`. |
| `variance` | enum | `unequal` | `unequal` (Welch) or `pooled` (Student, `var.equal = TRUE`); needs `split_by`. Detail: `test-variant-options`. |

- `weight` — moments on w*, N* = Σw (frequency) or Kish n_eff (probability); `df` = N*−1 (two-sample: Welch df on N*_g), may be fractional. `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (optional → two-sample Welch) — `categorical_u8`/`u16`/`u32` or `packed_bool` (levels `"0"`/`"1"`).

## Output

`statistic` = t; `df`; `p_value` two-sided. One-sample `details`: `mu`, `n`, `mean`, `variance`, `ci_low`/`ci_high`, `effect_size.cohens_d` = (mean − mu)/sd. Two-sample: per-group `n`/`mean`/`variance`, `diff`, CI, pooled-SD `cohens_d`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-sample needs exactly 2 `split_by` groups, else `PULSE_TEST_SPLIT_GROUPS_LT_2`.
- Constant `field` within a group → `PULSE_TEST_VARIANCE_ZERO`.
- n < 2 (per group) → `PULSE_TEST_INSUFFICIENT_N`.
- With `groups`: one result per group, each with `group_key`; `split_by` a grouper → `PULSE_TEST_SPLIT_GROUPS_LT_2`; a too-sparse group → `null` figures + a warning (`statistical-testing`).

## See

- `pulse_examples_search tags=[t-test]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-z-two-sample`, `op-test-paired-t`
