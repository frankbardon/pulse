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

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md).

## Use when

How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many.

Questions it answers:

- How many distinct things do these twelve rating questions actually measure?
- Is this set of measures correlated enough to be worth summarising into components?

Use something else:

- `MAT_CORRELATION` when you only want to see which measures go together, pair by pair.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `on` | `correlation` \| `covariance` | `correlation` | Matrix to decompose. |
| `components` | int \| `kaiser` \| `{variance: s}` | `kaiser` | Kept components: k (≤ p), λ > 1, or the fewest reaching share s. Required on `covariance` (no `kaiser`). |
| `repair` | `nearest` | none | Decompose the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), kind `rectangular`, `column_keys` `PC1`…`PCk`, always full. `auxiliary.eigenvectors` (same shape); pairwise `auxiliary.n`. `vectors`: `eigenvalues` (all p, descending), `explained_variance`, `cumulative`, `communalities`, `kmo_msa`. `scalars`: `kmo`, `bartlett_chisq`, `bartlett_df`, `bartlett_p`, `components_retained`. No rotation.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.
- `scalars.kmo`: The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Each eigenvector's largest entry is positive; flip a column freely.
- Bartlett's n: rows; Σw (frequency) or n_eff (probability); pairwise the smallest pair (`PULSE_MATRIX_PAIRWISE_N_STAR`).
- Singular correlation: KMO and Bartlett null (`PULSE_MATRIX_SINGULAR` warning). Non-PSD pairwise input: fatal `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-correlation`](op-mat-correlation.md), [`op-mat-reliability`](op-mat-reliability.md), [`weighting`](weighting.md)
