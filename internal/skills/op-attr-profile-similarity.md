---
name: op-attr-profile-similarity
description: Per-row similarity to a target profile — correlation, cosine or a set similarity across a vector's members, against a literal, label-map, registered, centroid, saved-population or stored-record reference.
kind: operator
category: ATTR
operator: ATTR_PROFILE_SIMILARITY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `metric` | enum | — | Similarity only: `correlation`, `cosine`, `jaccard`, `dice`, `overlap`. Absent: from the vector's `kind` (scale → `correlation`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A distance → `PULSE_VECTOR_METRIC_UNKNOWN`<!-- feature: ATTR_VEC_DISTANCE -->, `details.use` = `ATTR_VEC_DISTANCE`<!-- /feature -->. |
| `reference` | any | (required) | `[…]`, `{label: value}`, `{table, key}`, `{table, key_field}`, `{centroid: {filter?, by?}}`, `{population: name}` or `{record: {cohort?, keys}}` — forms and warnings: `vector-row-scores`. |
| `min_valid` | int | `1` | Fewest complete pairs; below → null. |
| `labels` | list | — | Inline `fields` only: one label per member. |

## Inputs

`vector` or `fields` (no `field`); members: see `## Contract`, source columns only.

## Output

One `f64` per record; null below `min_valid` or with no profile / centroid.

## Components

- `n_reference_missing`, `n_domain_invalid`.

<!-- generated: contract -->

## Gotchas

- Matched BY LABEL, never position: wrong labels / length → `PULSE_VECTOR_REFERENCE_MISMATCH` at predict.
- A null or profile-less key, or a domain violation, nulls the row and warns once (`PULSE_VECTOR_REFERENCE_MISSING`, `PULSE_VECTOR_METRIC_DOMAIN`); strict does not escalate. Unbound population → `PULSE_VECTOR_REFERENCE_UNKNOWN`; a failed compose fit → every row null + `PULSE_FIT_UNAVAILABLE`.
- A record's lookup refusals (`PULSE_INDEX_*`, `PULSE_LOOKUP_*`) refuse the run, never at predict.
- Row-local (streams, fuses, merges; weight ignored) — except a centroid: two-pass, weighted, cohort-wide, serial; a chain refuses it.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: `vector-row-scores`, `vector-metrics`, `multivariate-design`
