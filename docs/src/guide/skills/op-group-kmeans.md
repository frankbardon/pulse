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

| Name | Type | Default | Description |
|---|---|---|---|
| `k` | int | (required) | 2 ≤ k ≤ distinct tuples. |
| `seed` | int | `0` | int64; 0 = fixed. |
| `max_iter` | int | `100` | Lloyd iterations per start. |
| `n_init` | int | `10` | Seeded starts; lowest within-SS wins. |
| `standardize` | bool | `true` | z-score each member first. |
| `missing` | enum | `listwise` | `listwise` only. |
| `fit` | enum | `global` | `global` (once over the filtered set) or `within` (per bucket of the groupers before it; no-op outermost). A `within` refit that stops at `max_iter` warns once per bucket, `details.group_key` = the outer segment; the whole-set fit (Components) warns unstamped. |

`weight`: frequency only (a row is w points).

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Key column `GROUP_KMEANS_<vector>` or `_<first fields entry>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`.

## Output

Keys `cluster_1`…`cluster_k`, emitted largest first (ties: lexicographic centroid). A null member → `n_null`.

## Components

- The params, `members`, `center` / `scale`, `distinct_points`, `buckets` `{key, count, size, within_ss, centroid, centroid_standardized}`, `total_ss`, `within_ss`, `between_ss`, `between_ss_ratio`, `iterations`, `converged`, `best_init`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED` (k-means has no probability-weighted form: a sampling weight does not make a row several coincident points, and no reference implementation defines design-weighted clusters).
- Streaming: buffered only; components `none`. k-means fits over every row before any label exists; the request runs buffered and serially under DecodeWorkers / ShardWorkers, and a crosstab falls back to the buffered arm.
- Components: `total_n`, `n_null`, `k`, `seed`, `n_init`, `max_iter`, `standardize`, `members`, `center`, `scale`, `distinct_points`, `buckets`, `total_ss`, `within_ss`, `between_ss`, `between_ss_ratio`, `iterations`, `converged`, `best_init`.

## Gotchas

- Squared Euclidean only (the `sq_euclidean` registry metric); no `metric` param.
- Labels depend only on the member tuples, never on row or shard order.
- Buffered, never merged; predict `row_buffer_bytes` = n·p·8 + k·p·8.
- Best start at `max_iter`: `PULSE_MATRIX_NOT_CONVERGED` (solver `kmeans_lloyd`).
- `include` on `groups`: `PROCESSING_CONFIG`.

## See

- `pulse_examples_search tags=[multivariate]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-attr-mahalanobis`](op-attr-mahalanobis.md)
