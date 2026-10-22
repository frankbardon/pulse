```yaml
name: test-variant-options
description: The long per-variant option text the op-test-* Params tables point at — the CUPED `covariate` adjustment of the t-family and the two-proportion test, the `reference` level, and the chi-square `expected` goodness-of-fit map. Read once, shared by every test that offers the option.
type: guide
kind: design
applies_to: process, compose, predict
covers: [TEST, covariate, CUPED, reference, expected, goodness-of-fit, two_proportion_adjusted, variance_reduction]
```

# Test variant options

The `op-test-<name>` Params tables keep one short row per option; the contract that is the same across tests lives here. Choosing the test: [`statistical-testing`](statistical-testing.md).

## `covariate` — CUPED, the t-family tests

A numeric pre-period column `x`, one pass: θ = slope of `Field` on `x` pooled over both groups, and `diff`, its CI, the statistic, p, `mean` / `variance` and `cohens_d` read the adjusted `Field − θ(x − x̄)` — equal to the two-step (θ first, then the test on the adjusted column).

- `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction` (1 − SE²adjusted / SE²raw; with `theta` `null` when `x` is constant) and `n_covariate_dropped` (null-covariate rows, dropped listwise).
- Composes with `reference`. Weighted (both kinds) it is the weighted two-step: θ and x̄ on the weights, each group's N*. Grouped, θ is per group.
- Needs `SplitBy`; without it, non-numeric, or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too). The same code on any test that has no covariate form.

## `covariate` — the two-proportion test

A numeric pre-period column adjusting the success indicator I to I − θ(x − x̄), θ pooled over both groups (weighted: on the weights; grouped: per group). `Variant` `two_proportion_adjusted`: `proportion`, `diff`, Wald CI and `cohens_h` adjusted; z = `diff` / its Wald SE (θ = 0 gives the unadjusted interval). `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction`, `n_covariate_dropped`. With `p0` (one-proportion), non-numeric or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too).

## `reference` — the two-group tests

The `SplitBy` level read SECOND, so `diff`, its CI, the statistic and the signed effect size read other − reference (default: alphabetical, second level). A value that is not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`). On a `packed_bool` split the levels are `"0"` / `"1"`.

## `expected` — chi-square goodness of fit

`{level: proportion}` switches to goodness of fit (`Variant` `goodness_of_fit`) and `Cols` is omitted. Each proportion > 0, summing to 1 ± 1e-9 (never renormalised), at least 2 labels. A listed level never observed counts as 0; an observed level not listed → `PULSE_TEST_EXPECTED_LABEL_MISSING`. A bad map, one given with `Cols`, or a `packed_bool` label other than `"0"` / `"1"` → `PULSE_TEST_EXPECTED_INVALID` (predict too). The sample-ratio-mismatch check is `{"control": 0.5, "treatment": 0.5}`.

## See

- Skills: [`statistical-testing`](statistical-testing.md), [`weighting`](weighting.md), [`op-test-t`](op-test-t.md), [`op-test-welch`](op-test-welch.md), [`op-test-z-two-sample`](op-test-z-two-sample.md), [`op-test-prop-z`](op-test-prop-z.md), [`op-test-chisq`](op-test-chisq.md)
