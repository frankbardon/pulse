---
name: op-attr-profile-similarity
description: Per-row similarity to a target profile — correlation, cosine or a set similarity across a vector's members, against a literal, label-map or registered reference.
kind: operator
category: ATTR
operator: ATTR_PROFILE_SIMILARITY
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

- `metric` — similarity only: `correlation`, `cosine`, `jaccard`, `dice`, `overlap`. Default from the vector's `kind` (scale → `correlation`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A distance → `PULSE_VECTOR_METRIC_UNKNOWN`<!-- feature: ATTR_VEC_DISTANCE -->, `details.use` = `ATTR_VEC_DISTANCE`<!-- /feature -->.
- `reference` (required) — `[…]` (member order), `{label: value}`, `{table, key}` or `{table, key_field}` (a categorical column).
- `min_valid` — fewest complete pairs, default 1; below → null.
- `labels` — inline `fields` only.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`; no `field`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record; null below `min_valid` or with no profile.

## Components

Floor `{n, n_null}` + `n_reference_missing`, `n_domain_invalid`. `Mergeable`.

## Gotchas

- Matched BY LABEL, never position: wrong labels / length → `PULSE_VECTOR_REFERENCE_MISMATCH` at predict.
- A `key_field` label with no profile is null and counted.
- Row-local: streams, fuses; weight ignored.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: `vector-metrics`, `multivariate-design`
