---
name: op-test-welch
description: Explicit two-sample Welch t-test on a numeric field across a categorical split_by partition.
kind: operator
category: TEST
operator: TEST_WELCH
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, t-test, tier-1-test, parametric, two-sample, welch, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `reference` | string | — | The `split_by` level read second: `diff` reads other − reference (default: alphabetical). Codes: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period field (CUPED): the test runs on `y − θ(x − x̄)`; needs `split_by`. Detail: `test-variant-options`. |

- `weight` — per-group moments on w*, Welch–Satterthwaite df on N*_g (Σw frequency, Kish n_eff probability; fractional). `details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, exactly 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`statistic` = t on the unpooled SE √(s²_a/n_a + s²_b/n_b); `df` = Welch-Satterthwaite; `p_value` two-sided Student-t (R `t.test(var.equal = FALSE)`). `details`: `groups`, per-arm `n` / `mean` / `variance` (sample, n − 1), `diff`, `ci_low`/`ci_high` on `diff`, pooled-SD `effect_size.cohens_d`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

<!-- feature: TEST_T -->
- Identical math to `TEST_T` with `split_by`; this alias documents intent.
<!-- /feature -->
- Welch denominator never assumes equal variance — the safe default when spreads may differ.
- The Welford triple is byte-equal to the Welford aggregate's on the same inputs<!-- feature: OVERLAY_T_CELL --> — reuse via `OVERLAY_T_CELL` on crosstabs<!-- /feature -->.
- Constant `field` within a group → `PULSE_TEST_VARIANCE_ZERO`.
- No `split_by` → `PROCESSING_CONFIG` (predict too).

## See

- `pulse_examples_search tags=[welch]`
- Skills: `statistical-testing`, `op-test-t`, `op-test-z-two-sample`, `op-test-mann-whitney-u`
