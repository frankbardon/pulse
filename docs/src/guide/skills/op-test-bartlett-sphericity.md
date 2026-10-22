```yaml
name: op-test-bartlett-sphericity
description: Bartlett's sphericity test on a vector's correlation matrix (is it the identity?); the factorability check, bit-equal to the PCA operator's Bartlett scalars.
kind: operator
category: TEST
operator: TEST_BARTLETT_SPHERICITY
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, multivariate, streaming-friendly]
```

Tests emit statistic / p-value; no `Response.Components`.

## Use when

Checks whether a set of numeric fields are related at all, the first check before summarising them as a few underlying dimensions.

Questions it answers:

- Do the ten satisfaction items move together enough to be worth reducing to a few scores?
- Are these sensor readings related at all, or is each one independent noise?

Use something else:

- `MAT_CORRELATION` when you want how strongly each pair is related.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `missing` | `listwise` | `listwise` | Missing-data mode; only listwise (a row with any null member is dropped). |


Slot: exactly one of `vector` (a `vectors` entry) / `fields`, ≥ 2 members; no `field`, `vector2` / `fields2` or `split_by`.
- `weight` — both kinds; N* = Σw or n_eff.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

`Statistic` = χ² = −ln|R|·(N* − 1 − (2p + 5)/6), `DF` = p(p − 1)/2, `PValue`. `Details`: `members`, `n`, `n_dropped`, `determinant` (|R|).

## Reading the output

- `statistic`: A chi-square that grows as the fields' correlations move away from all zero; a small p-value says the fields are related enough to summarise as fewer dimensions.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.determinant`: The determinant of the correlation matrix, 1 when the fields are uncorrelated and near 0 when they are strongly related; the statistic grows as it shrinks.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- Streamable. Same rows ⇒ same bits as the PCA operator's Bartlett scalars (`MAT_PCA`).
- Singular R: `PULSE_MATRIX_SINGULAR`; N* ≤ 1 + (2p + 5)/6: `PULSE_MATRIX_INSUFFICIENT_N`. Both warn, null figures.
- Large n rejects almost always.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-mat-pca`](op-mat-pca.md), [`statistical-testing`](statistical-testing.md)
