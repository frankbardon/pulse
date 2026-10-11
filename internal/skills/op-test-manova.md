---
name: op-test-manova
description: One-way MANOVA of a mean vector of p numeric members across a SplitBy of 2+ levels; Pillai's trace headline, with Wilks, Hotelling–Lawley and Roy in details.
kind: operator
category: TEST
operator: TEST_MANOVA
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, streaming-friendly]
---

Tests emit statistic / p-value; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `alpha` — float, default `0.05`. `missing` — `listwise` only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (inline, globs ok); no `field`, no `vector2` / `fields2`. `split_by` required, categorical, ≥ 2 levels.
- `weight` — both kinds; per-level moments on w*, N*_g = Σw or n_eff. `Details` add per-level `sum_weights` (+ `n_eff`).
<!-- feature: capability:multiplicity -->
- `multiplicity` — one site, Pillai's p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = Pillai's V, `DF` = its df1, `PValue` its p. `Details`: `f`, `df1`, `df2` (Pillai), `members`, `groups` (sorted), `n`, `n_dropped`, `means`, `df_hypothesis` (k − 1), `df_residual` (N* − k), `eigenvalues` (of E⁻¹H), and `pillai` / `wilks` / `hotelling_lawley` / `roy`, each `{statistic, f, df1, df2, p_value}`; `roy.upper_bound` is true. `Variant`: `one_way`.

<!-- generated: reading-the-output -->

## Gotchas

- Streamable (one co-moment per level). Needs n ≥ p + k complete rows; 1 level is `PULSE_TEST_SPLIT_GROUPS_LT_2`.
- Singular within SSCP: null figures + `PULSE_MATRIX_SINGULAR` warning.
- Roy's p is a lower bound. A null `p_value` means R would print NA.
- 2 levels: all four share one exact F<!-- feature: TEST_HOTELLING_T2 --> (= pooled `TEST_HOTELLING_T2`)<!-- /feature -->.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `statistical-testing`, `multivariate-design`, `op-test-hotelling-t2`, `op-test-anova-f`
