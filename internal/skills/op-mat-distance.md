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

- `between` — required grouper spec as in `groups`, extension groupers too; no own `weight`.
- `metric` — a `matrix.metrics` name with consumer `MAT_DISTANCE`: `euclidean` (default; over a `kind` vector the kind's default, `vector-metrics`), `sq_euclidean`, `manhattan`, `chebyshev`, `cosine` (1 − cos), `hellinger`, `aitchison`, `mahalanobis` (pooled covariance, raw centroids). `standardize` — z-score first; refused with `hellinger` / `aitchison`.
- `min_valid` — fewest complete member pairs per distance (default 1); below it null.
- `missing` (`listwise`), `max_drop_share`, `weight`, `encoding`: `op-mat-correlation`. `fit` is refused (`fit_unsupported`: centroids are not one fitted state).

## Inputs

Integer / float members.

## Output

`primary`: k × k, bucket-label keys, zero diagonal. `auxiliary.centroids` (k × p); `vectors.sizes`, `counts`.

## Components

- `metric`, `standardize`, `buckets`, `n_null_key`; `min_pair_n` / `max_pair_n` never (listwise).

<!-- generated: contract -->

## Gotchas

- Streams when `between` does; a whole-set or extension grouper buffers.
<!-- feature: GROUP_DATE, GROUP_DATE_RANGES -->
- Date-family between: its `tz`, else `time_zone`.
<!-- /feature -->
<!-- feature: GROUP_KMEANS -->
- `GROUP_KMEANS`: buffered, frequency weights only.
<!-- /feature -->
- Missing coordinate (zero-spread member under standardize): dropped pairwise, sums rescaled by p/p_observed; `mahalanobis` needs all.
- Negative coordinate under `hellinger` / `aitchison`, or an aitchison zero: null cells, `PULSE_MATRIX_METRIC_DOMAIN`.
- An explicit metric unsuited to the vector's `kind` (`cosine` on scale / measure, Euclidean on composition) warns `PULSE_VECTOR_METRIC_UNSUITED`; error under strict.
- Massless bucket: null centroid (`PULSE_MATRIX_INSUFFICIENT_N`); collinear: `PULSE_MATRIX_SINGULAR`. Centres only; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `multivariate-outputs`, `vector-metrics`
<!-- feature: GROUP_KMEANS -->
- `op-group-kmeans`
<!-- /feature -->
