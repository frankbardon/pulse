---
name: op-mat-pca
description: Principal component analysis of a battery — loadings and eigenvectors (p × k, rectangular), every eigenvalue with explained / cumulative shares, communalities, KMO (overall and per member) and Bartlett's sphericity test; correlation or covariance input, listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_PCA
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `on` | `correlation` \| `covariance` | `correlation` | Matrix to decompose. |
| `components` | int \| `kaiser` \| `{variance: s}` | `kaiser` | Kept components: k (≤ p), λ > 1, or the fewest reaching share s. Required on `covariance` (no `kaiser`). |
| `repair` | `nearest` | none | Decompose the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As `op-mat-correlation`. |

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), kind `rectangular`, `column_keys` `PC1`…`PCk`, always full. `auxiliary.eigenvectors` (same shape); pairwise `auxiliary.n`. `vectors`: `eigenvalues` (all p, descending), `explained_variance`, `cumulative`, `communalities`, `kmo_msa`. `scalars`: `kmo`, `bartlett_chisq`, `bartlett_df`, `bartlett_p`, `components_retained`. No rotation.

<!-- generated: reading-the-output -->

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- Each eigenvector's largest entry is positive; flip a column freely.
- Bartlett's n: rows; Σw (frequency) or n_eff (probability); pairwise the smallest pair (`PULSE_MATRIX_PAIRWISE_N_STAR`).
- Singular correlation: KMO and Bartlett null (`PULSE_MATRIX_SINGULAR` warning). Non-PSD pairwise input: fatal `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `op-mat-reliability`, `weighting`
