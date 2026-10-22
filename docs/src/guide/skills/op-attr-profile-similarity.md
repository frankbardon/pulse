```yaml
name: op-attr-profile-similarity
description: Per-row similarity to a target profile — correlation, cosine or a set similarity across a vector's members, against a literal, label-map, registered, centroid, saved-population or stored-record reference.
kind: operator
category: ATTR
operator: ATTR_PROFILE_SIMILARITY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
```

## Use when

Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target.

Questions it answers:

- How closely does each respondent's brand ratings follow the ideal loyalist profile?
- How similar is each store's channel mix to its region's target mix?

Use something else:

- `ATTR_VEC_DISTANCE` when you want how far apart the values are rather than how alike the pattern is.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `metric` | enum | — | Similarity only: `correlation`, `cosine`, `jaccard`, `dice`, `overlap`. Absent: from the vector's `kind` (scale → `correlation`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A distance → `PULSE_VECTOR_METRIC_UNKNOWN`, `details.use` = `ATTR_VEC_DISTANCE`. |
| `reference` | any | (required) | `[…]`, `{label: value}`, `{table, key}`, `{table, key_field}`, `{centroid: {filter?, by?}}`, `{population: name}` or `{record: {cohort?, keys}}` — forms and warnings: [`vector-row-scores`](vector-row-scores.md). |
| `min_valid` | int | `1` | Fewest complete pairs; below → null. |
| `labels` | list | — | Inline `fields` only: one label per member. |

## Inputs

`vector` or `fields` (no `field`); members: see `## Contract`, source columns only.

## Output

One `f64` per record; null below `min_valid` or with no profile / centroid.

## Components

- `n_reference_missing`, `n_domain_invalid`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `n_reference_missing`, `n_domain_invalid`.

## Gotchas

- Matched BY LABEL, never position: wrong labels / length → `PULSE_VECTOR_REFERENCE_MISMATCH` at predict.
- A null or profile-less key, or a domain violation, nulls the row and warns once (`PULSE_VECTOR_REFERENCE_MISSING`, `PULSE_VECTOR_METRIC_DOMAIN`); strict does not escalate. Unbound population → `PULSE_VECTOR_REFERENCE_UNKNOWN`; a failed compose fit → every row null + `PULSE_FIT_UNAVAILABLE`.
- A record's lookup refusals (`PULSE_INDEX_*`, `PULSE_LOOKUP_*`) refuse the run, never at predict.
- Row-local (streams, fuses, merges; weight ignored) — except a centroid: two-pass, weighted, cohort-wide, serial; a chain refuses it.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`vector-row-scores`](vector-row-scores.md), [`vector-metrics`](vector-metrics.md), [`multivariate-design`](multivariate-design.md)
