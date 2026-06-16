---
name: op-test-shapiro-wilk
description: Shapiro-Wilk normality test on Field; runs per-group when SplitBy is set; supports n ≤ 5000.
kind: operator
category: TEST
operator: TEST_SHAPIRO_WILK
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, distribution-shape, parametric, buffered-pipeline]
---

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric); `SplitBy` (optional categorical — when set, runs the test per-group).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` (optional) |

## Output

`Statistic` = W (Shapiro-Wilk W, `(0, 1]`); `PValue` (small p ⇒ reject normality). With `SplitBy`: `Details.per_group` carries per-arm W and p; headline `Statistic` / `PValue` track the worst-rejecting group.

## Gotchas

- Buffered — requires the ordered values.
- Supports `n ≤ 5000`; larger n emits `PULSE_TEST_SHAPIRO_N_BOUND` and skips. Fall back to QQ inspection or `TEST_KS` vs fitted normal.
- Pre-ANOVA gate: per-group rejection → switch `TEST_ANOVA_F` → `TEST_KRUSKAL_WALLIS`.
- Tiny groups (`n < 3`) → `PULSE_TEST_INSUFFICIENT_N`.
- Tier-2 variant `TEST_SHAPIRO_WILK/shapiro_francia_post` runs on a result column.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-kruskal-wallis`, `op-test-ks`
