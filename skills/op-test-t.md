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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |
| `mu` | float | `0.0` | Hypothesized mean (one-sample only; ignored when `SplitBy` is set). |

Slot params: `Field` (required, numeric); `SplitBy` (optional categorical → switches to two-sample Welch).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`TestResult.Statistic` = t; `DF`; `PValue` (two-sided, Student-t CDF); `RejectNull` = `PValue < Alpha`. `Details` carries per-group `{n, mean, variance}` for the two-sample variant.

## Gotchas

- Two-sample variant requires exactly 2 SplitBy groups; else `PULSE_TEST_INVALID_SPLITBY`.
- Streamable — reads running Welford state from a parallel `AGG_WELFORD` on the same `(field, split_by)`.
- Constant Field within a group → `PULSE_TEST_VARIANCE_ZERO`.
- Tiny groups → unstable p; gate with `AGG_COUNT` + `PULSE_TEST_INSUFFICIENT_N`.
- Unambiguous two-sample intent → `TEST_WELCH`; large-n survey conventions → `TEST_Z_TWO_SAMPLE`.

## See

- `pulse_examples_search tags=[t-test]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-z-two-sample`, `op-test-paired-t`
