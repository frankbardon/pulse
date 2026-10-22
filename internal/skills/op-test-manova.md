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

No `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `missing` | `listwise` | `listwise` | Missing-data mode; only listwise (a row with any null member is dropped). |


Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (inline, globs ok); no `field`, no `vector2` / `fields2`. `split_by` required, categorical or `packed_bool` (levels `"0"`/`"1"`), ≥ 2 levels.
- `weight` — both kinds (`Details` add `sum_weights`).

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = Pillai's V, `DF` = its df1, `PValue` its p. `Details`: Pillai's `f`, `df1`, `df2`, `members`, `groups`, `n`, `means`, `eigenvalues`, and `pillai` / `wilks` / `hotelling_lawley` / `roy`, each `{statistic, f, df1, df2, p_value}` (`multivariate-outputs`). `Variant`: `one_way`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Streamable. Needs n ≥ p + k rows (Σw under `frequency`); 1 level is `PULSE_TEST_SPLIT_GROUPS_LT_2`.
- Singular within SSCP: null figures + `PULSE_MATRIX_SINGULAR`.
- Roy's p is a lower bound; a null `p_value` means R prints NA.
- 2 levels: all four share one F<!-- feature: TEST_HOTELLING_T2 --> (= pooled `TEST_HOTELLING_T2`)<!-- /feature -->.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: `statistical-testing`, `multivariate-outputs`, `op-test-hotelling-t2`, `op-test-anova-f`
