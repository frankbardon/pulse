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

- `metric` — similarity only: `correlation`, `cosine`, `jaccard`, `dice`, `overlap`. Default from the vector's `kind` (scale → `correlation`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A distance → `PULSE_VECTOR_METRIC_UNKNOWN`, `details.use` = `ATTR_VEC_DISTANCE`.
- `reference` (required) — `[…]` (member order), `{label: value}`, `{table, key}`, `{table, key_field}` (categorical or `u4`…`u64`, by exact base-10 form; `u64` above 2^53 too), `{centroid: {filter?, by?}}` (weighted per-member mean of the rows `filter` admits, = `AGG_VEC_MEAN`; `by` = per exact key, each row vs its own) `{population: name}` (a bound vector fit's mean: [`compose-fits`](compose-fits.md)) or `{record: {cohort?, keys}}` (one row point-looked-up via its cohort's index, the request's own if omitted: [`cohort-sidecar-index`](cohort-sidecar-index.md)).
- `min_valid` — fewest complete pairs, default 1; below → null.
- `labels` — inline `fields` only.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

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
- A null or profile-less `key_field` / `by` row is null and warns `PULSE_VECTOR_REFERENCE_MISSING` (`n_key_null`, `n_key_unmatched`); a domain violation `PULSE_VECTOR_METRIC_DOMAIN`. Once per slot; strict does not escalate.
- Unbound population → `PULSE_VECTOR_REFERENCE_UNKNOWN`; a compose fit that failed on its data → every row null + `PULSE_FIT_UNAVAILABLE`.
- A record is read by member column (null = missing element); a member its cohort lacks → `PULSE_VECTOR_REFERENCE_MISMATCH` `record_members_absent`; lookup refusals (`PULSE_INDEX_*`, `PULSE_LOOKUP_*`) refuse the run, never at predict.
- Row-local (streams, fuses, weight ignored) — except a centroid: two-pass, weighted, cohort-wide (grouped runs buffered). Merges (not a centroid): shards / parallel decode fold its per-run tallies (counts and warnings once, cohort-wide); a chain stage admits it. A centroid runs serially; a chain refuses it.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`vector-metrics`](vector-metrics.md), [`multivariate-design`](multivariate-design.md)
