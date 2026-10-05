---
name: op-test-t
description: One-sample or two-sample Welch t-test on a numeric field; SplitBy switches to the two-sample variant.
kind: operator
category: TEST
operator: TEST_T
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, t-test, tier-1-test, parametric, two-sample, one-sample, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `mu` — float, default `0.0`. Hypothesized mean, one-sample only; ignored when `SplitBy` is set.
- `weight` — slot weight (`null` opts out), both kinds: moments on w*, N* = Σw (frequency) or Kish n_eff (probability); `df` = N*−1 (two-sample: Welch df on N*_g), may be fractional. `Details` add `sum_weights` (+ `n_eff`, probability) shaped like `n` (raw rows); n_eff < 2 warns `PULSE_WEIGHT_LOW_NEFF`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` (required) — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (optional → two-sample Welch) — `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = t; `DF`; `PValue` two-sided. One-sample `Details`: `mu`, `n`, `mean`, `variance`, `ci_low`/`ci_high`, `effect_size.cohens_d` = (mean − mu)/sd. Two-sample: per-group `n`/`mean`/`variance`, `diff`, CI, pooled-SD `cohens_d`.

## Gotchas

- Two-sample needs exactly 2 `SplitBy` groups, else `PULSE_TEST_INVALID_SPLITBY`.
- Streamable — running Welford state per `SplitBy` group.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.
- n < 2 (per group) → `PULSE_TEST_INSUFFICIENT_N`.

## See

- `pulse_examples_search tags=[t-test]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-z-two-sample`, `op-test-paired-t`
