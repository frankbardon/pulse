```yaml
name: op-group-kmeans
description: Segment records into k clusters by seeded, deterministic k-means over a vector of numeric members; keys cluster_1..cluster_k by descending size.
kind: operator
category: GROUP
operator: GROUP_KMEANS
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, buffered-pipeline]
```

## Use when

Finds k segments of rows that are alike across several numbers at once, such as customer types by spend, visits and tenure.

Questions it answers:

- What natural customer segments do spend, visits and tenure suggest?
- Which respondent types stand out across the rating battery?

Use something else:

- `GROUP_CATEGORY` when you already know the groups.
- `GROUP_QUANTILE` when you want bands of one number.

## Params

- `k` (required, 2 ≤ k ≤ distinct tuples). `seed` int64 (0 = fixed). `max_iter` 100, `n_init` 10 (lowest within-SS wins). `standardize` true. `missing` `listwise`.
- `weight`: frequency only (a row is w points).

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Key column `GROUP_KMEANS_<vector>` or `_<first fields entry>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

Keys `cluster_1`…`cluster_k`, emitted largest first (ties: lexicographic centroid). A null member → `n_null`.

## Components

`{total_n, n_null}`, the params, `members`, `center` / `scale`, `distinct_points`, `buckets` `{key, count, size, within_ss, centroid, centroid_standardized}`, `total_ss`, `within_ss`, `between_ss`, `between_ss_ratio`, `iterations`, `converged`, `best_init`. Mergeability `None`.

## Gotchas

- Labels depend only on the member tuples, never on row or shard order.
- Buffered, never merged; predict `row_buffer_bytes` = n·p·8 + k·p·8.
- Best start at `max_iter`: `PULSE_MATRIX_NOT_CONVERGED` (solver `kmeans_lloyd`).

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-attr-mahalanobis`](op-attr-mahalanobis.md)
