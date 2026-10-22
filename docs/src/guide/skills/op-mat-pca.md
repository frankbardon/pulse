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

- `on` — `correlation` (default) or `covariance`. `repair` — `nearest`.
- `rotation` (k ≥ 2) — `none`, `varimax`, `quartimax`, `geomin_orthogonal` (`epsilon`, 0.01), `entropy`. Oblique names refused: `rotation_oblique_unsupported`.
- `components` — int k (≤ p), `kaiser` (λ > 1, default) or `{variance: s}`; required on `covariance`.
- `missing`, `max_drop_share`, `fit`, `weight`, `encoding`: [`op-mat-correlation`](op-mat-correlation.md).

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), `PC1`…`PCk`. `auxiliary`: `eigenvectors`, `n` (pairwise), `unrotated`. `vectors`: `eigenvalues`, `explained_variance`, `cumulative`, `communalities`, `kmo_msa`, `rotated_variance`. `scalars`: `kmo`, `bartlett_*`, `components_retained`.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.
- `scalars.kmo`: The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Signs are free. Rotated: read `rotated_variance`.
- Bartlett's n: rows, Σw or n_eff, pairwise the smallest pair's own. A pairwise weighted covariance scales each entry by its own pair.
- Singular: KMO, Bartlett null (`PULSE_MATRIX_SINGULAR`). Non-PSD pairwise: `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md), [`op-mat-correlation`](op-mat-correlation.md)
