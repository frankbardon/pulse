---
name: op-mat-distance
description: Distances between segment centroids — a k × k matrix over the buckets a grouper (params.between: any built-in, date family included, or a registered extension grouper) cuts the rows into; euclidean, sqeuclidean, manhattan, chebyshev, cosine or mahalanobis (pooled within-bucket covariance); optional standardize; weighted centroids.
kind: operator
category: MAT
operator: MAT_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
---

Slot and members as for `op-mat-covariance`; the members are the centroid coordinates.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `between` | grouper spec | required | `{type, field \| vector \| fields, params, include, interval}` as in `groups`, extension groupers too; no own `weight`; `tz` only on a date-family grouper. |
| `metric` | `euclidean` \| `sqeuclidean` \| `manhattan` \| `chebyshev` \| `cosine` \| `mahalanobis` | `euclidean` | `stats::dist` forms; cosine = 1 − cos; mahalanobis over the pooled within-bucket covariance, on raw centroids. |
| `standardize` | bool | `false` | z-score members over the bucketed rows first (R `scale()`). |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | `listwise` only; rest as `op-mat-correlation`. |

The metric list is local; U27's shared metric registry folds it in later.

## Inputs

Integer / float members.

## Output

`primary`: k × k, `square_symmetric`, keyed by bucket label in the grouper's order<!-- feature: GROUP_KMEANS --> (`GROUP_KMEANS`: `cluster_1`… by size)<!-- /feature -->, zero diagonal. `auxiliary.centroids` (k × p, `rectangular`, rows buckets), `centroids_standardized` (standardize), `pooled_covariance` (mahalanobis). `vectors.sizes` (Σw per bucket), `counts` (rows).

## Components

`n`, `n_null`, `n_listwise_dropped` (`min_pair_n` / `max_pair_n` never: listwise only)<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->; `metric`, `standardize`, `buckets`, `n_null_key` (rows in no bucket, excluded).

## Gotchas

- Streams and merges when `between` does; a whole-set or extension grouper buffers and runs serially.
<!-- feature: GROUP_DATE, GROUP_DATE_RANGES -->
- A date-family between reads its zone like a top-level grouper: its `tz`, else the request `time_zone`; predict echoes it at slot `matrices[i].params.between`.
<!-- /feature -->
<!-- feature: GROUP_KMEANS -->
- A `GROUP_KMEANS` between buffers and takes frequency weights only.
<!-- /feature -->
- Massless bucket: null centroid, `PULSE_MATRIX_INSUFFICIENT_N` scope `buckets`; mahalanobis with N* ≤ k: scope `pooled`; collinear members: `PULSE_MATRIX_SINGULAR`, distances null.
- Distances describe centres, not overlap; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `multivariate-design`, `matrix-results`
<!-- feature: GROUP_KMEANS -->
- `op-group-kmeans`
<!-- /feature -->
