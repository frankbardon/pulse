```yaml
name: op-attr-mahalanobis
description: Per-row Mahalanobis distance D² (or its chi-square p) across a vector of numeric members, from a cohort-wide fit; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_MAHALANOBIS
type: reference
applies_to: process, compose, predict
examples_tags: [outlier-detection, multivariate, streaming-friendly]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together.

Questions it answers:

- Which respondents gave an unusual pattern of answers across the whole battery, even if no single answer is extreme?
- Which transactions have an odd combination of amount, frequency and basket size?

Use something else:

- `ATTR_ZSCORE` when only one field matters.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `output` | enum | `d2` | `d2` or `p` (χ² upper tail on p df). |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` ([`vector-row-scores`](vector-row-scores.md)). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

`weight` — both kinds; fit only ([`vector-row-scores`](vector-row-scores.md)).

Slot: `vector` or `fields`, no `field` ([`vector-row-scores`](vector-row-scores.md)). Default label `ATTR_MAHALANOBIS_<vector>` or `_<first fields entry>`.

## Inputs

Members: see `## Contract` (`packed_bool` via `coerce: binary`); source columns only.

## Output

One `f64` per record: D² = (x − μ)ᵀS⁻¹(x − μ) ≥ 0, or its p. A null member → null.

## Reading the output

- `value`: With output d2 (the default): the row's squared Mahalanobis distance D² = (x - mean)' S^-1 (x - mean) across the p fields, the distance from the fitted average row in units of the fields' own spread and correlation; 0 is exactly average and it is never negative. With output p: the chance of a D² at least this large under a chi-square curve on p degrees of freedom, so a small value marks an unusual row.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass (buffered beside groupers / features); `fit` is row-local and merges; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` ([`vector-row-scores`](vector-row-scores.md)).
- Singular fit or N* ≤ p: all scores null + `PULSE_MATRIX_SINGULAR` / `PULSE_MATRIX_INSUFFICIENT_N` warning.

## See

- `pulse_examples_search tags=[outlier-detection, multivariate]`
- Skills: [`vector-row-scores`](vector-row-scores.md), [`attribute-composition`](attribute-composition.md), [`multivariate-design`](multivariate-design.md), [`op-attr-zscore`](op-attr-zscore.md)
