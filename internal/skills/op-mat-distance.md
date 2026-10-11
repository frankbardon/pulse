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

As `op-mat-covariance`; members are centroid coordinates.

<!-- generated: use-when -->

## Params

- `between` — required grouper spec as in `groups`, extension groupers too; no own `weight`.
- `metric` — `euclidean` (default), `sqeuclidean`, `manhattan`, `chebyshev`, `cosine`, `mahalanobis`. `standardize` — z-score first.
- `missing` (`listwise`), `max_drop_share`, `weight`, `encoding`: `op-mat-correlation`.

## Inputs

Integer / float members.

## Output

`primary`: k × k, bucket-label keys, zero diagonal. `auxiliary.centroids` (k × p); `vectors.sizes`, `counts`.

## Components

`n`, `n_null`, `n_listwise_dropped`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->; `metric`, `standardize`, `buckets`, `n_null_key`; `min_pair_n` / `max_pair_n` never (listwise).

## Gotchas

- Streams when `between` does; a whole-set or extension grouper buffers.
<!-- feature: GROUP_DATE, GROUP_DATE_RANGES -->
- Date-family between: its `tz`, else `time_zone`.
<!-- /feature -->
<!-- feature: GROUP_KMEANS -->
- `GROUP_KMEANS`: buffered, frequency weights only.
<!-- /feature -->
- Massless bucket: null centroid (`PULSE_MATRIX_INSUFFICIENT_N`); collinear: `PULSE_MATRIX_SINGULAR`. Centres only; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `multivariate-outputs`
<!-- feature: GROUP_KMEANS -->
- `op-group-kmeans`
<!-- /feature -->
