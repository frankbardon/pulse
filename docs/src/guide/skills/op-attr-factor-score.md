```yaml
name: op-attr-factor-score
description: Per-row score on one common factor of a vector of numeric members, from an inline fit with the factor-analysis matrix operator's knobs, loadings, order and sign; regression or Bartlett scores; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_FACTOR_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, streaming-friendly]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together.

Questions it answers:

- What is each respondent's score on the satisfaction factor behind these rating questions?
- Can I score every row on the two constructs a factor analysis found, to compare segments on them?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `factor` | int | (required) | 1-based factor to score. |
| `method` | enum | `regression` | `regression` or `bartlett`. |
| `factors` | any | `kaiser` | Positive integer k (≥ `factor`) or `"kaiser"`. |
| `extraction` | enum | `minres` | `minres`, `principal_axis` or `ml`. |
| `rotation` | enum | `none` | Orthogonal or oblique (its factor correlation enters the weights). |
| `kappa` | float | `4` | Promax power. |
| `gamma` | float | `0` | Oblimin gamma. |
| `epsilon` | float | `0.01` | Geomin epsilon. |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` ([`vector-row-scores`](vector-row-scores.md)). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

The fit knobs are `MAT_FACTOR`'s factor fit's; no `repair`. `weight` — fit only.

## Inputs

`vector` or `fields` (no `field`); members: see `## Contract`, source columns only.

## Output

One `f64` per record: zᵀw on fit-standardized members (psych `factor.scores`); mean 0 over the fit rows; null member → null.

## Reading the output

- `value`: The row's estimated score on the chosen factor, on the scale of the fields standardized over the fitted rows: 0 is an average row, positive is toward the end where the fields loading positively on the factor are high.
  - Caveat: The direction is MAT_FACTOR's convention (each factor's loadings sum positive), not a meaning: check its loadings on the same fields before calling a high score good or bad.
  - Caveat: After an oblique rotation (promax, oblimin, geomin) the factors correlate, so scores on two factors move together.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass, `fit` row-local; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` ([`vector-row-scores`](vector-row-scores.md)).
- All null + warning: `PULSE_MATRIX_SINGULAR`, `_INSUFFICIENT_N`, `_NOT_IDENTIFIED`, `_HEYWOOD` (Bartlett). Scored + warning: regression Heywood, `_NOT_CONVERGED`.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: [`vector-row-scores`](vector-row-scores.md), [`multivariate-outputs`](multivariate-outputs.md) (causes), [`op-mat-factor`](op-mat-factor.md), [`op-attr-pc-score`](op-attr-pc-score.md)
