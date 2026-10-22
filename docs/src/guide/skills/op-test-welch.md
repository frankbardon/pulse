```yaml
name: op-test-welch
description: Explicit two-sample Welch t-test on a numeric field across a categorical SplitBy partition.
kind: operator
category: TEST
operator: TEST_WELCH
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, t-test, tier-1-test, parametric, two-sample, welch, streaming-friendly]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread.

Questions it answers:

- Do customers in the two pricing arms spend different amounts on average?
- Is average delivery time different between the two depots?

Use something else:

- `TEST_PAIRED_T` when the same subjects are measured twice, such as before and after.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `reference` — string: the `SplitBy` level read second, so `diff`, CI, t and `cohens_d` read other − reference (default: alphabetical, second level). Not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`).
- `covariate` — string, a numeric pre-period column (CUPED), one pass: θ = slope of `Field` on it pooled over both groups, and `diff`, CI, t, p, `mean` / `variance` and `cohens_d` read the adjusted `Field − θ(x − x̄)` (equal to the two-step: θ first, then the test on the adjusted column). `Details` add `diff_unadjusted`, `theta`, `covariate_mean`, `variance_reduction` (1 − SE²adjusted / SE²raw; with `theta` `null` when x is constant) and `n_covariate_dropped` (null-covariate rows, dropped listwise). Composes with `reference`; weighted (both kinds) it is the weighted two-step (θ, x̄ on the weights; each group's N*); grouped, θ is per group. On any other test, without `SplitBy`, non-numeric or `= Field` → `PULSE_TEST_COVARIATE_INVALID` (predict too).

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).
- `weight` — per-group moments on w*, Welch–Satterthwaite df on N*_g (Σw frequency, Kish n_eff probability; fractional). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = t; `DF` = Welch-Satterthwaite; `PValue` two-sided Student-t. `Details.per_group` = `{n, mean, variance}` per arm; effect size = mean diff / pooled SE.

## Reading the output

- `statistic`: Welch's t: the first group's mean minus the second's, in standard errors computed from each group's own variance. Values far from 0 in either direction are unlikely if the true gap is zero.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.diff`: The first group's mean minus the second's, in the field's own units.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Fixed-horizon p only: no sequential / always-valid p-values, no Bayesian readout ([`statistical-testing`](statistical-testing.md)).
- Identical math to `TEST_T` with `SplitBy`; this alias documents intent.
- Welch denominator never assumes equal variance — the safe default when spreads may differ.
- The Welford triple is byte-equal to the Welford aggregate's on the same inputs — reuse via `OVERLAY_T_CELL` on crosstabs.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.
- No `Field` or no `SplitBy` → `PROCESSING_CONFIG` (predict too).
- Fallback without `covariate` (a test that refuses it): the two-step in one call — an `ATTR_FORMULA` column `y - ((total(1)*total(y*x) - total(y)*total(x)) / (total(1)*total(x*x) - total(x)*total(x))) * (x - total(x)/total(1))` under the plain test. Caveats: each `total()` skips its own nulls, so filter `y` and `x` non-null first or the sums misalign; a probability weight is refused (`total()` is frequency-only); no `theta` / `variance_reduction`; its pre-pass reads the rows twice (`covariate` is one pass).

## See

- `pulse_examples_search tags=[welch]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-t`](op-test-t.md), [`op-test-z-two-sample`](op-test-z-two-sample.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md)
