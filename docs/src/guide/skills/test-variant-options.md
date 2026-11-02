```yaml
name: test-variant-options
description: The long per-variant option text the op-test-* Params tables point at — the CUPED `covariate` adjustment of the t-family and the two-proportion test (and its formula fallback), the one-proportion `p0` test, the `reference` level, the chi-square `expected` goodness-of-fit map, and the methodological options `variance`, `exact`, `correct` and `interval`. Read once, shared by every test that offers the option.
type: guide
kind: design
applies_to: process, compose, predict
covers: [TEST, covariate, CUPED, p0, one_proportion, reference, expected, goodness-of-fit, two_proportion_adjusted, variance_reduction, pooled, var.equal, exact, Yates, continuity correction, Wilson]
```

# Test variant options

The `op-test-<name>` Params tables keep one short row per option; the contract that is the same across tests lives here. Choosing the test: [`statistical-testing`](statistical-testing.md).

## `covariate` — CUPED, the t-family tests

A numeric pre-period column `x`, one pass: θ = slope of `Field` on `x` pooled over both groups, and `diff`, its CI, the statistic, p, `mean` / `variance` and `cohens_d` read the adjusted `Field − θ(x − x̄)` — equal to the two-step (θ first, then the test on the adjusted column).

- `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction` (1 − SE²adjusted / SE²raw; with `theta` `null` when `x` is constant) and `n_covariate_dropped` (null-covariate rows, dropped listwise).
- Composes with `reference`. Weighted (both kinds) it is the weighted two-step: θ and x̄ on the weights, each group's N*. Grouped, θ is per group.
- Needs `SplitBy`; without it, non-numeric, or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too). The same code on any test that has no covariate form.

**Without `covariate`** (a test that refuses it): the two-step in one call — an `ATTR_FORMULA` column `y - ((total(1)*total(y*x) - total(y)*total(x)) / (total(1)*total(x*x) - total(x)*total(x))) * (x - total(x)/total(1))` under the plain test. Caveats: each `total()` skips its own nulls, so filter `y` and `x` non-null first or the sums misalign; a probability weight is refused (`total()` is frequency-only); no `theta` / `variance_reduction`; its pre-pass reads the rows twice (`covariate` is one pass).

## `covariate` — the two-proportion test

A numeric pre-period column adjusting the success indicator I to I − θ(x − x̄), θ pooled over both groups (weighted: on the weights; grouped: per group). `Variant` `two_proportion_adjusted`: `proportion`, `diff`, Wald CI and `cohens_h` adjusted; z = `diff` / its Wald SE (θ = 0 gives the unadjusted interval). `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction`, `n_covariate_dropped`. With `p0` (one-proportion), non-numeric or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too).

## `p0` — the one-proportion test

`p0` in `(0, 1)` with `SplitBy` absent tests one proportion against it (`Variant` `one_proportion`): z = (p̂ − p0)/√(p0(1−p0)/N*). `Details` are scalar `n`, `successes`, `proportion`, plus `p0`, `diff` = p̂ − p0 and a Wald `ci_low` / `ci_high` on `diff` (`interval` `wilson` for `prop.test`'s); `cohens_h` is against p0. Out of range or beside `SplitBy` → `PULSE_TEST_P0_INVALID` (predict too).

## `reference` — the two-group tests

The `SplitBy` level read SECOND, so `diff`, its CI, the statistic and the signed effect size read other − reference (default: alphabetical, second level). A value that is not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`). On a `packed_bool` split the levels are `"0"` / `"1"`.

## `variance` — pooled two-sample t

The t-test only (its Welch alias stays Welch). `pooled`: Student's t on s²_p = ((n_a − 1)s²_a + (n_b − 1)s²_b)/(n_a + n_b − 2), SE = s_p·√(1/n_a + 1/n_b), df = n_a + n_b − 2 — R's `t.test(var.equal = TRUE)`. `Variant` `pooled_two_sample`; `Details` add `pooled_variance`. Weighted: n is N*, s² on w* (frequency: the expanded rows). With `covariate` the adjusted moments pool alike. Without `SplitBy`, or another value → `PROCESSING_CONFIG` (predict too).

## `exact` — Mann-Whitney exact p

`true`: p = min(2·P(T ≤ t), 2·P(T ≥ t), 1) over the permutation distribution of group a's rank sum on the pooled mid-ranks — ties included, as R 4.6's `wilcox.test(exact = TRUE)`; `Variant` `exact`, no continuity correction (`z` / `var_u` stay the approximation's). Frequency weight: the expanded rows. Past min(n_a, n_b)·(n_a + n_b) > 5000 the normal p stays and `PULSE_TEST_EXACT_UNAVAILABLE` warns.

## `correct` — continuity corrections

χ² test: Yates on a 2 × 2 independence table, R's `chisq.test(correct = TRUE)`: Y = min(0.5, min |O − E|), χ² = Σ(|O − E| − Y)²/E, `Details` add `continuity_correction` (Y); `cramers_v` / `phi` stay uncorrected; inert on larger tables and goodness of fit. Proportion z-test: `prop.test(correct = TRUE)` — Y = min(0.5, D), D the table's |O − E| (two-proportion: |Δ|/(1/n_a + 1/n_b); one: |x − N*·p0|); z becomes z·(D − Y)/D and the interval widens (Wald by Y·Σ1/n, one-proportion Wald by Y/N*, Wilson by shifting p̂ ∓ Y/n). Refused with `covariate`. Weighted: on the w* table (frequency: the expanded rows).

## `interval` — one-proportion Wilson

Proportion z-test with `p0`: `wilson` is `prop.test`'s score interval on p̂, clipped to [0, 1], reported on p̂ − p0 (`ci_low` / `ci_high`, as the Wald one); `Details` add `interval`. On N* when weighted. Without `p0` → `PROCESSING_CONFIG` (predict too).

## `expected` — chi-square goodness of fit

`{level: proportion}` switches to goodness of fit (`Variant` `goodness_of_fit`) and `Cols` is omitted. Each proportion > 0, summing to 1 ± 1e-9 (never renormalised), at least 2 labels. A listed level never observed counts as 0; an observed level not listed → `PULSE_TEST_EXPECTED_LABEL_MISSING`. A bad map, one given with `Cols`, or a `packed_bool` label other than `"0"` / `"1"` → `PULSE_TEST_EXPECTED_INVALID` (predict too). The sample-ratio-mismatch check is `{"control": 0.5, "treatment": 0.5}`.

## See

- Skills: [`statistical-testing`](statistical-testing.md), [`weighting`](weighting.md), [`op-test-t`](op-test-t.md), [`op-test-welch`](op-test-welch.md), [`op-test-z-two-sample`](op-test-z-two-sample.md), [`op-test-prop-z`](op-test-prop-z.md), [`op-test-chisq`](op-test-chisq.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md)
