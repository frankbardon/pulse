---
name: op-test-z-two-sample
description: Two-sample z-test on Field means across two SplitBy groups; identical SE to TEST_WELCH but p-value via standard normal Φ.
kind: operator
category: TEST
operator: TEST_Z_TWO_SAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, two-sample, z, proportion-analysis, streaming-friendly]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |

## Output

`Statistic` = z (= Welch t numerator over the same pooled SE); `PValue` two-sided via standard normal Φ; no `DF`. `Details.per_group` = `{n, mean, variance}` per arm.

## Gotchas

- Streamable — reads the same per-group Welford buckets as `TEST_T` / `TEST_WELCH`.
- Statistic + SE byte-equal to `TEST_WELCH`; **p-value differs** (Φ vs Student-t). For small n the divergence is non-trivial; predict surfaces no warning — choose intentionally.
- Use only when n is large per group AND survey conventions demand normal-CDF p.
- Default Student-t inference → `TEST_WELCH`.
- Pairs with `OVERLAY_Z_CELL` / `OVERLAY_Z_VS_REF` on crosstab cells.

## See

- `pulse_examples_search tags=[z]`
- Skills: `statistical-testing`, `op-test-welch`, `op-test-prop-z`, `overlay-system`
