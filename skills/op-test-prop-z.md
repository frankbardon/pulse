---
name: op-test-prop-z
description: Two-proportion z-test on the success rate of Field across two SplitBy groups; streamable via success / total counts.
kind: operator
category: TEST
operator: TEST_PROP_Z
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, proportion-analysis, experiment-analysis, streaming-friendly]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |
| `success` | string | required | Dictionary value of Field treated as a "success". |

Slot params: `Field` (required, categorical / `packed_bool`), `SplitBy` (required, categorical, exactly 2 groups).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`Statistic` = z (pooled SE under H₀); `PValue` two-sided via standard normal Φ. `Details.per_group` = `{n, successes, rate}` per arm; effect size = rate diff (with optional CI in `Details.ci`).

## Gotchas

- `success` must match a dictionary value; otherwise `PULSE_TEST_INVALID_SUCCESS`.
- Streamable — per-group counts feed both numerator and pooled denominator in one pass.
- Small expected counts → switch to `TEST_FISHER_EXACT` (still 2×2).
- For k > 2 SplitBy groups use `TEST_CHISQ` on the implicit `(SplitBy × Field)` contingency.
- Pairs with `OVERLAY_PROP_Z_CELL` for crosstab cell-level proportion tests.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `statistical-testing`, `op-test-chisq`, `op-test-fisher-exact`
