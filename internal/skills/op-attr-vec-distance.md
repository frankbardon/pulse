---
name: op-attr-vec-distance
description: Per-row distance to a target profile — plain, set or fitted (std_euclidean, mahalanobis) metrics across a vector's members, against a literal, label-map, registered, centroid or saved-population reference.
kind: operator
category: ATTR
operator: ATTR_VEC_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

- `metric` — distance only: `euclidean`, `sq_euclidean`, `manhattan`, `chebyshev`, `cosine` (1 − cos), `hellinger`, `aitchison`, `hamming`, `std_euclidean`, `mahalanobis`. Default from `kind` (measure → `std_euclidean`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A similarity → `PULSE_VECTOR_METRIC_UNKNOWN`<!-- feature: ATTR_PROFILE_SIMILARITY -->, `details.use` = `ATTR_PROFILE_SIMILARITY`<!-- /feature -->.
- `reference` (required) — `[…]`, `{label: value}`, `{table, key}`, `{table, key_field}` (categorical or `u4`…`u64`, by base-10 form), `{centroid: {filter?, by?}}` (weighted per-member mean of the rows `filter` admits<!-- feature: AGG_VEC_MEAN -->, = `AGG_VEC_MEAN`<!-- /feature -->; `by` = per-key) or `{population: name}` (a bound fit's mean; need not be the `fit`).
- `min_valid` — fewest complete pairs, default 1. `labels` — inline `fields` only.
- `fit_on`, `missing`, `fit` — fitted metrics only; `fit` reads a bound vector fit (a compose `fits` entry or loaded `populations` entry: `compose-fits`) by label instead of fitting (XOR `fit_on`; a regression's name `PULSE_MODEL_KIND_MISMATCH`).

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; exactly one of `vector` / `fields`.

## Output

One `f64` ≥ 0 per record; sums rescaled by p / p_observed.

## Components

Floor `{n, n_null}` + `n_reference_missing`, `n_domain_invalid`. `Mergeable`.

## Gotchas

- `std_euclidean` / `mahalanobis`: two-pass fit (listwise, weighted under a weight); N* ≤ p or singular → all null + warning. With `fit`: row-local, one pass, streams beside groupers; never merged, like its row-local metrics (shards run serially; a chain stage refuses it).
- A centroid reference is two-pass for every metric (and keeps a `fit` slot two-pass); weighted, cohort-wide.
- Matched BY LABEL: `PULSE_VECTOR_REFERENCE_MISMATCH` at predict; unbound population `PULSE_VECTOR_REFERENCE_UNKNOWN`.
- A null / profile-less key (`key_field` or `by`) or a domain violation (`hellinger`, `aitchison`) nulls the row and warns once per slot: `PULSE_VECTOR_REFERENCE_MISSING`, `PULSE_VECTOR_METRIC_DOMAIN`; strict does not escalate.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: `vector-metrics`, `multivariate-design`
