```yaml
name: op-mat-partial-correlation
description: Partial correlation matrix of a vector's numeric members — each pair with every other member, or the params.control fields, held fixed — listwise or pairwise, weighted; non-PSD input refused unless repair "nearest", singular input refused; p-values and confidence intervals on request (params.inference).
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
| `inference` | bool | `false` | `auxiliary.p` / `ci_lower` / `ci_upper`, df = N* − 2 − g. |
| `alpha` | (0, 1) | `0.05` | Interval level; needs `inference`. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float members and controls.

## Output

`primary`: partial r over the non-control members, diagonal 1; all null when an input r is undefined. Pairwise `auxiliary.n`; `warnings`. `inference`: per pair p (t on N* − 2 − g, g = controls, = ppcor) and Fisher-z interval (se 1/√(N* − 3 − g)); diagonal null.

## Reading the output

- `primary.values`: Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.
  - Caveat: Correlation is not causation: a third factor may drive both fields.
- `auxiliary.p`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Components

Over members + outside controls: `n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Non-PSD pairwise input: FATAL `PULSE_MATRIX_NOT_PSD`; `repair: "nearest"` (Higham) warns with `frobenius_adjustment`.
- Collinear fields: `PULSE_MATRIX_SINGULAR` (`dependent_fields`).
- `inference` N*: listwise the fold's; pairwise the smallest pair's (`PULSE_MATRIX_PAIRWISE_N_STAR`). Too thin: null + `PULSE_MATRIX_INSUFFICIENT_N` scope `inference`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-correlation`](op-mat-correlation.md), [`weighting`](weighting.md)
