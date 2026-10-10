```yaml
name: op-mat-partial-correlation
description: Partial correlation matrix of a vector's numeric members — each pair with every other member, or the params.control fields, held fixed — listwise or pairwise, weighted; non-PSD input refused unless repair "nearest", singular input refused.
kind: operator
category: MAT
operator: MAT_PARTIAL_CORRELATION
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md).

## Use when

How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1.

Questions it answers:

- Does satisfaction still track price once delivery time is held fixed?
- Which of these ratings are linked directly, rather than only through the overall score?

Use something else:

- `MAT_CORRELATION` when you want each pair's link with nothing held fixed.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `control` | `"all"` \| [fields] | `all` | Held-fixed set; a listed member leaves the axis, an outside field joins the fold. |
| `repair` | `nearest` | none | Repair non-PSD input. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float members and controls.

## Output

`primary`: partial r over the non-control members, diagonal 1; all null when an input r is undefined. Pairwise `auxiliary.n`; `warnings`. No p-values.

## Reading the output

- `primary.values`: Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.
  - Bands (Cohen (1988), absolute value): very small below 0.1; small 0.1 to 0.3; medium 0.3 to 0.5; large 0.5 and above.
  - Sign: positive means the two fields tend to rise together; negative means one field tends to fall as the other rises.

## Components

Over members + outside controls: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Non-PSD pairwise input: FATAL `PULSE_MATRIX_NOT_PSD`; `repair: "nearest"` (Higham) warns with `frobenius_adjustment`.
- Collinear fields: `PULSE_MATRIX_SINGULAR` (`dependent_fields`).

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-correlation`](op-mat-correlation.md), [`weighting`](weighting.md)
