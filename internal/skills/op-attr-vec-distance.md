---
name: op-attr-vec-distance
description: Per-row distance to a target profile — plain, set or fitted (std_euclidean, mahalanobis) metrics across a vector's members, against a literal, label-map, registered, centroid, saved-population or stored-record reference.
kind: operator
category: ATTR
operator: ATTR_VEC_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `metric` | enum | — | Distance only: `euclidean`, `sq_euclidean`, `manhattan`, `chebyshev`, `cosine`, `hellinger`, `aitchison`, `hamming`, `std_euclidean`, `mahalanobis`. Absent: from `kind`; none → `PULSE_VECTOR_METRIC_REQUIRED`. A similarity → `PULSE_VECTOR_METRIC_UNKNOWN`<!-- feature: ATTR_PROFILE_SIMILARITY -->, `details.use` = `ATTR_PROFILE_SIMILARITY`<!-- /feature -->. |
| `reference` | any | (required) | `[…]`, `{label: value}`, `{table, key}`, `{table, key_field}`, `{centroid: {filter?, by?}}`, `{population: name}` or `{record: {cohort?, keys}}` — forms and warnings: `vector-row-scores`. |
| `min_valid` | int | `1` | Fewest complete pairs. |
| `labels` | list | — | Inline `fields`: one label per member. |
| `fit_on` | any | `filtered` | Fitted metrics: filterers picking the FIT rows. |
| `fit` | string | — | Fitted metrics: a bound vector fit; XOR `fit_on` (`vector-row-scores`). |
| `missing` | enum | `listwise` | Fitted metrics; `listwise` only. |

## Inputs

`vector` or `fields` (no `field`); members: see `## Contract`.

## Output

One `f64` ≥ 0 per record.

## Components

- `n_reference_missing`, `n_domain_invalid`.

<!-- generated: contract -->

## Gotchas

- `std_euclidean` / `mahalanobis` fit first: two-pass, listwise, weighted (with `fit`: streams, never merges); N* ≤ p or singular → all null + warning.
- Every other metric is row-local and merges; a centroid reference is two-pass for any metric.
- Matched BY LABEL (`PULSE_VECTOR_REFERENCE_MISMATCH` at predict). A missing key, a domain violation (`hellinger`, `aitchison`) or a failed fit nulls rows with a warning (`vector-row-scores`).

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: `vector-row-scores`, `vector-metrics`, `multivariate-design`
