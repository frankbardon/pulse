```yaml
name: op-mat-distance
description: Distances between segment centroids — a k × k matrix over the buckets a grouper (params.between, any non-date built-in) cuts the rows into; euclidean, sqeuclidean, manhattan, chebyshev, cosine or mahalanobis (pooled within-bucket covariance); optional standardize; weighted centroids.
kind: operator
category: MAT
operator: MAT_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
```

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md); the members are the centroid coordinates.

## Use when

How far apart segments are across several numbers at once: the gap between each pair of segment centres.

Questions it answers:

- Which customer segments are most alike on spend, visits and tenure, and which stand furthest apart?
- How far apart are the regions' average profiles across the rating battery?

Use something else:

- `TEST_MANOVA` when you want to test whether the centres differ.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `between` | grouper spec | required | `{type, field \| vector \| fields, params, include, interval}` as in `groups`; no date-family grouper; no own `weight` / `tz`. |
| `metric` | `euclidean` \| `sqeuclidean` \| `manhattan` \| `chebyshev` \| `cosine` \| `mahalanobis` | `euclidean` | `stats::dist` forms; cosine = 1 − cos; mahalanobis over the pooled within-bucket covariance, on raw centroids. |
| `standardize` | bool | `false` | z-score members over the bucketed rows first (R `scale()`). |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | `listwise` only; rest as [`op-mat-correlation`](op-mat-correlation.md). |

The metric list is local; U27's shared metric registry folds it in later.

## Inputs

Integer / float members.

## Output

`primary`: k × k, `square_symmetric`, keyed by bucket label in the grouper's order (`GROUP_KMEANS`: `cluster_1`… by size), zero diagonal. `auxiliary.centroids` (k × p, `rectangular`, rows buckets), `centroids_standardized` (standardize), `pooled_covariance` (mahalanobis). `vectors.sizes` (Σw per bucket), `counts` (rows).

## Components

`n`, `n_null`, `n_listwise_dropped` (`min_pair_n` / `max_pair_n` never: listwise only); weighted `sum_weights`, `n_eff`, `n_weight_invalid`; `metric`, `standardize`, `buckets`, `n_null_key` (rows in no bucket, excluded).

## Gotchas

- Streams and merges when `between` does; a whole-set grouper buffers and runs serially.
- A `GROUP_KMEANS` between buffers and takes frequency weights only.
- Massless bucket: null centroid, `PULSE_MATRIX_INSUFFICIENT_N` scope `buckets`; mahalanobis with N* ≤ k: scope `pooled`; collinear members: `PULSE_MATRIX_SINGULAR`, distances null.
- Distances describe centres, not overlap; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: [`multivariate-design`](multivariate-design.md), [`matrix-results`](matrix-results.md)
- [`op-group-kmeans`](op-group-kmeans.md)
