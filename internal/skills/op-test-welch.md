---
name: op-test-welch
description: Explicit two-sample Welch t-test on a numeric field across a categorical SplitBy partition.
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
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `RejectNull` when p < alpha. |
| `reference` | string | — | The `SplitBy` level read second: `diff` reads other − reference (default: alphabetical). Codes: `test-variant-options`. |
| `covariate` | string | — | A numeric pre-period column (CUPED): the test runs on `Field − θ(x − x̄)`; needs `SplitBy`. Detail: `test-variant-options`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).
- `weight` — per-group moments on w*, Welch–Satterthwaite df on N*_g (Σw frequency, Kish n_eff probability; fractional). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = t on the unpooled SE √(s²_a/n_a + s²_b/n_b); `DF` = Welch-Satterthwaite; `PValue` two-sided Student-t (R `t.test(var.equal = FALSE)`). `Details`: `groups`, per-arm `n` / `mean` / `variance` (sample, n − 1), `diff`, `ci_low`/`ci_high` on `diff`, pooled-SD `effect_size.cohens_d`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

<!-- feature: TEST_T -->
- Identical math to `TEST_T` with `SplitBy`; this alias documents intent.
<!-- /feature -->
- Welch denominator never assumes equal variance — the safe default when spreads may differ.
- The Welford triple is byte-equal to the Welford aggregate's on the same inputs<!-- feature: OVERLAY_T_CELL --> — reuse via `OVERLAY_T_CELL` on crosstabs<!-- /feature -->.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.
- No `Field` or no `SplitBy` → `PROCESSING_CONFIG` (predict too).

## See

- `pulse_examples_search tags=[welch]`
- Skills: `statistical-testing`, `op-test-t`, `op-test-z-two-sample`, `op-test-mann-whitney-u`
