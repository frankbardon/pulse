---
name: op-mat-distance
description: Distances between segment centroids — a k × k matrix over the buckets a grouper (params.between: any built-in, date family included, or a registered extension grouper) cuts the rows into; any registry metric MAT_DISTANCE accepts (euclidean, sq_euclidean, manhattan, chebyshev, cosine, hellinger, aitchison, mahalanobis); optional standardize; weighted centroids.
kind: operator
category: MAT
operator: MAT_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
---

As `op-mat-covariance`; members are centroid coordinates.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `between` | object | (required) | A grouper spec as in `groups` (extension groupers too); no own `weight`. |
| `metric` | enum | `euclidean` | A `matrix.metrics` distance (consumer `MAT_DISTANCE`); a `kind` vector's default (`vector-metrics`). |
| `min_valid` | int | `1` | Fewest complete member pairs per distance; below → null. |
| `standardize` | bool | `false` | z-score members over the bucketed rows (R `scale()`) first; refused with `hellinger` / `aitchison`. |
| `missing` | `listwise` | `listwise` | Listwise only (pairwise refused). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |

`weight`, `encoding`: as `op-mat-correlation`. `fit` is refused (`fit_unsupported`: centroids are not one fitted state). Layout and metric detail: `multivariate-outputs`.

## Inputs

Integer / float members.

## Output

`primary`: k × k, bucket-label keys, zero diagonal. `auxiliary.centroids` (k × p); `vectors.sizes`, `counts`.

<!-- generated: contract -->

## Gotchas

- Streams when `between` does; a whole-set or extension grouper buffers.
<!-- feature: GROUP_KMEANS -->
- `GROUP_KMEANS`: buffered, frequency weights only.
<!-- /feature -->
- A missing coordinate (zero-spread member under standardize) drops from that distance; `mahalanobis` needs all.
- Negative coordinate under `hellinger` / `aitchison`, or an aitchison zero: null cells, `PULSE_MATRIX_METRIC_DOMAIN`.
- Massless bucket: null centroid (`PULSE_MATRIX_INSUFFICIENT_N`); collinear: `PULSE_MATRIX_SINGULAR`. Centres only; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `multivariate-outputs`, `vector-metrics`, `op-group-kmeans`
