```yaml
name: op-test-manova
description: One-way MANOVA of a mean vector of p numeric members across a SplitBy of 2+ levels; Pillai's trace headline, with Wilks, Hotelling–Lawley and Roy in details.
kind: operator
category: TEST
operator: TEST_MANOVA
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, streaming-friendly]
```

No `Response.Components`.

## Use when

Checks whether several numeric fields, taken together as one profile, differ across two or more groups.

Questions it answers:

- Do our customer segments differ on the whole set of spend, visit and basket metrics at once?
- Do the three store formats differ on their combined service ratings?

Use something else:

- `TEST_ANOVA_F` when there is only one numeric field.

## Params

- `alpha` — float, default `0.05`. `missing` — `listwise` only.

Slot: exactly one of `vector` (a `vectors` entry) / `fields` (inline, globs ok); no `field`, no `vector2` / `fields2`. `split_by` required, categorical or `packed_bool` (levels `"0"`/`"1"`), ≥ 2 levels.
- `weight` — both kinds (`Details` add `sum_weights`).

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = Pillai's V, `DF` = its df1, `PValue` its p. `Details`: Pillai's `f`, `df1`, `df2`, `members`, `groups`, `n`, `means`, `eigenvalues`, and `pillai` / `wilks` / `hotelling_lawley` / `roy`, each `{statistic, f, df1, df2, p_value}` ([`multivariate-outputs`](multivariate-outputs.md)). `Variant`: `one_way`.

## Reading the output

- `statistic`: Pillai's trace, between 0 and the smaller of the field count and the group count minus one, sums how much of the fields' joint spread lies between the group average profiles; larger values are stronger evidence that the profiles differ; the p-value says whether it is large enough to be surprising.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable. Needs n ≥ p + k rows (Σw under `frequency`); 1 level is `PULSE_TEST_SPLIT_GROUPS_LT_2`.
- Singular within SSCP: null figures + `PULSE_MATRIX_SINGULAR`.
- Roy's p is a lower bound; a null `p_value` means R prints NA.
- 2 levels: all four share one F (= pooled `TEST_HOTELLING_T2`).

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`statistical-testing`](statistical-testing.md), [`multivariate-outputs`](multivariate-outputs.md), [`op-test-hotelling-t2`](op-test-hotelling-t2.md), [`op-test-anova-f`](op-test-anova-f.md)
