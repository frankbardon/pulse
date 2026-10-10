```yaml
name: op-mat-reliability
description: Scale reliability of a battery — Cronbach's alpha, standardized alpha, McDonald's omega (one-factor minres), mean inter-item r, per-item item-total r / alpha if deleted / mean / sd — with reverse-keyed items flipped before the fold; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_RELIABILITY
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md); at least 2 items.

## Use when

How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest.

Questions it answers:

- Are these five satisfaction questions reliable enough to average into one score?
- Which item in this battery fits the others worst and should be dropped?

Use something else:

- `MAT_CORRELATION` when you only want to see which items go together, pair by pair.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `reverse` | [fields] | none | Reverse-keyed items: x' = min + max − x before the fold. |
| `scale_min`, `scale_max` | number | none | Battery range; both or neither, required with `reverse`. |
| `repair` | `nearest` | none | Fit omega on the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float items.

## Output

`primary`: inter-item correlation. `scalars`: `alpha`, `alpha_standardized`, `omega`, `mean_inter_item_r`. `vectors` (axis order): `item_total_r` (corrected), `alpha_if_deleted` (null at 2 items), `item_mean`, `item_sd`. Pairwise `auxiliary.n`; `warnings`. No p-values.

omega = (Σλ)² / ((Σλ)² + Σψ) from the minres fit (the model form, not psych::omega's observed-total `omega.tot`, which reads lower on a misfit battery).

## Reading the output

- `scalars.alpha`: Cronbach's alpha: how consistently the items measure one shared quality, from the share of the summed score's spread that the items share. 1 means the items move in lockstep; it can fall below 0 when items pull against each other, which usually means a reverse-worded item was not reversed.
  - Caveat: A high alpha does not show the items measure only one thing.
- `scalars.alpha_standardized`: Alpha computed on the items' correlations instead of their raw spreads: the alpha the battery would have if every item were first put on the same scale.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`; `iterations`, `converged` when the omega fit ran.

## Gotchas

- `reverse` without the range, or an item value outside it: `PROCESSING_CONFIG` (never clamped or inferred).
- omega null + warning: 2 items (`PULSE_MATRIX_NOT_IDENTIFIED`), a Heywood fit (`PULSE_MATRIX_HEYWOOD`), non-PSD pairwise input without `repair` (`PULSE_MATRIX_NOT_PSD`). Alpha always computes.
- Negative alpha usually means an unreversed item.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-correlation`](op-mat-correlation.md), [`weighting`](weighting.md)
