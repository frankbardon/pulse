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

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).
- `weight` — slot weight (`null` opts out), both kinds: per-group moments on w*, Welch–Satterthwaite df on N*_g (Σw frequency, Kish n_eff probability; fractional). `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = t; `DF` = Welch-Satterthwaite; `PValue` two-sided Student-t. `Details.per_group` = `{n, mean, variance}` per arm; effect size = mean diff / pooled SE.

## Reading the output

- `statistic`: Welch's t: the first group's mean minus the second's, in standard errors computed from each group's own variance. Values far from 0 in either direction are unlikely if the true gap is zero.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.diff`: The first group's mean minus the second's, in the field's own units.

## Gotchas

- Identical math to `TEST_T` with `SplitBy`; this alias documents intent.
- Welch denominator never assumes equal variance — the safe default when spreads may differ.
- The Welford triple is byte-equal to the Welford aggregate's on the same inputs — reuse via `OVERLAY_T_CELL` on crosstabs.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.

## See

- `pulse_examples_search tags=[welch]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-t`](op-test-t.md), [`op-test-z-two-sample`](op-test-z-two-sample.md), [`op-test-mann-whitney-u`](op-test-mann-whitney-u.md)
