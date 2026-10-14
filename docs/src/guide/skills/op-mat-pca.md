```yaml
name: op-mat-pca
description: Principal component analysis of a battery — loadings and eigenvectors (p × k, rectangular), every eigenvalue with explained / cumulative shares, communalities, KMO (overall and per member) and Bartlett's sphericity test; correlation or covariance input, listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_PCA
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

As [`op-mat-covariance`](op-mat-covariance.md).

## Use when

How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many.

Questions it answers:

- How many distinct things do these twelve rating questions actually measure?
- Is this set of measures correlated enough to be worth summarising into components?

Use something else:

- `MAT_CORRELATION` when you only want to see which measures go together, pair by pair.

## Params

- `on` — `correlation` (default) or `covariance`. `rotation` — `none`, `varimax` (k ≥ 2). `repair` — `nearest`.
- `components` — int k (≤ p), `kaiser` (λ > 1, default) or `{variance: s}`; required on `covariance`.
- `missing`, `max_drop_share`, `fit`, `weight`, `encoding`: [`op-mat-correlation`](op-mat-correlation.md).

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), `PC1`…`PCk`. `auxiliary`: `eigenvectors`, `n` (pairwise), `unrotated`. `vectors`: `eigenvalues`, `explained_variance`, `cumulative`, `communalities`, `kmo_msa`, `rotated_variance`. `scalars`: `kmo`, `bartlett_*`, `components_retained`.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.
- `scalars.kmo`: The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Signs are free. Rotated: read `rotated_variance`.
- Bartlett's n: rows, Σw or n_eff, pairwise the smallest pair.
- Singular: KMO, Bartlett null (`PULSE_MATRIX_SINGULAR`). Non-PSD pairwise: `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md), [`op-mat-correlation`](op-mat-correlation.md)
