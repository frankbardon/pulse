---
name: op-test-anova-f
description: One-way ANOVA F-test comparing the means of a numeric Field across k groups defined by SplitBy.
kind: operator
category: TEST
operator: TEST_ANOVA_F
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, k-sample, parametric, comparison, streaming-friendly]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, ≥ 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = F; `DF` = k−1; `PValue` via F survival. `Details`: `groups`, `n`, `group_means`, `ss_between`, `ss_within`, `df_within`, `ms_within`; `effect_size.{eta_squared, omega_squared}` (ω² clamped ≥0).

## Gotchas

- Streamable — per-group Welford feeds both SS terms.
- Rejects globally, not per pair<!-- feature: TEST_TUKEY_HSD --> — follow with tier-2 `TEST_TUKEY_HSD` via `ms_within` / `df_within`<!-- /feature -->.
- Assumes equal spread; a large p from a spread test is not evidence of it.
- Normality: a normality test describes shape; it is not a gate.
- Tier-2 `one_way_from_summary` reads group summaries; same keys.

## See

- `pulse_examples_search tags=[k-sample]`
- Skills: `statistical-testing`, `op-test-tukey-hsd`, `op-test-anova-welch`, `op-test-kruskal-wallis`
