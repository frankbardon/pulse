```yaml
name: op-mat-distance
description: Distances between segment centroids — a k × k matrix over the buckets a grouper (params.between: any built-in, date family included, or a registered extension grouper) cuts the rows into; any registry metric MAT_DISTANCE accepts (euclidean, sq_euclidean, manhattan, chebyshev, cosine, hellinger, aitchison, mahalanobis); optional standardize; weighted centroids.
kind: operator
category: MAT
operator: MAT_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
```

As [`op-mat-covariance`](op-mat-covariance.md); members are centroid coordinates.

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
| `between` | object | (required) | A grouper spec as in `groups` (extension groupers too); no own `weight`. |
| `metric` | enum | `euclidean` | A `matrix.metrics` distance (consumer `MAT_DISTANCE`); a `kind` vector's default ([`vector-metrics`](vector-metrics.md)). |
| `min_valid` | int | `1` | Fewest complete member pairs per distance; below → null. |
| `standardize` | bool | `false` | z-score members over the bucketed rows (R `scale()`) first; refused with `hellinger` / `aitchison`. |
| `missing` | `listwise` | `listwise` | Listwise only (pairwise refused). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |

`weight`, `encoding`: as [`op-mat-correlation`](op-mat-correlation.md). `fit` is refused (`fit_unsupported`: centroids are not one fitted state). Layout and metric detail: [`multivariate-outputs`](multivariate-outputs.md).

## Inputs

Integer / float members.

## Output

`primary`: k × k, bucket-label keys, zero diagonal. `auxiliary.centroids` (k × p); `vectors.sizes`, `counts`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`, `metric`, `standardize`, `buckets`, `n_null_key`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Streams when `between` does; a whole-set or extension grouper buffers.
- `GROUP_KMEANS`: buffered, frequency weights only.
- A missing coordinate (zero-spread member under standardize) drops from that distance; `mahalanobis` needs all.
- Negative coordinate under `hellinger` / `aitchison`, or an aitchison zero: null cells, `PULSE_MATRIX_METRIC_DOMAIN`.
- Massless bucket: null centroid (`PULSE_MATRIX_INSUFFICIENT_N`); collinear: `PULSE_MATRIX_SINGULAR`. Centres only; no p-values.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md), [`vector-metrics`](vector-metrics.md), [`op-group-kmeans`](op-group-kmeans.md)
