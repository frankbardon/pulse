```yaml
name: op-attr-vec-distance
description: Per-row distance to a target profile — plain, set or fitted (std_euclidean, mahalanobis) metrics across a vector's members, against a literal, label-map, registered, centroid, saved-population or stored-record reference.
kind: operator
category: ATTR
operator: ATTR_VEC_DISTANCE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, multivariate, streaming-friendly]
```

## Use when

Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread.

Questions it answers:

- How far is each store's KPI profile from the benchmark store, with every KPI on its own scale?
- Which respondents' ratings sit furthest from the target profile?

Use something else:

- `ATTR_MAHALANOBIS` when the target is the cohort's own centre.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `metric` | enum | — | Distance only: `euclidean`, `sq_euclidean`, `manhattan`, `chebyshev`, `cosine` (1 − cos), `hellinger`, `aitchison`, `hamming`, `std_euclidean`, `mahalanobis`. Absent: from `kind` (measure → `std_euclidean`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A similarity → `PULSE_VECTOR_METRIC_UNKNOWN`, `details.use` = `ATTR_PROFILE_SIMILARITY`. |
| `reference` | any | (required) | `[…]`, `{label: value}`, `{table, key}`, `{table, key_field}` (categorical or `u4`…`u64`, by exact base-10 form; `u64` above 2^53 too), `{centroid: {filter?, by?}}` (weighted per-member mean of the rows `filter` admits, = `AGG_VEC_MEAN`; `by` = per exact key) `{population: name}` (a bound fit's mean; need not be the `fit`) or `{record: {cohort?, keys}}` (one indexed row, own cohort if omitted: [`cohort-sidecar-index`](cohort-sidecar-index.md)). |
| `min_valid` | int | `1` | Fewest complete pairs. |
| `labels` | list | — | Inline `fields` only: one label per member. |
| `fit_on` | any | `filtered` | Fitted metrics only: `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | Fitted metrics only: a bound vector fit (a compose `fits` entry or loaded `populations` entry: [`compose-fits`](compose-fits.md)) read by label instead of fitting (XOR `fit_on`; a regression's name `PULSE_MODEL_KIND_MISMATCH`). |
| `missing` | enum | `listwise` | Fitted metrics only; `listwise` only. |

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; exactly one of `vector` / `fields`.

## Output

One `f64` ≥ 0 per record; sums rescaled by p / p_observed.

## Components

- `n_reference_missing`, `n_domain_invalid`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `n_reference_missing`, `n_domain_invalid`.

## Gotchas

- `std_euclidean` / `mahalanobis`: two-pass fit (listwise, weighted under a weight); N* ≤ p or singular → all null + warning. With `fit`: row-local, one pass, streams beside groupers; never merged (shards run serially; a chain stage refuses it).
- A row-local metric (no centroid) merges: shards / parallel decode fold its per-run tallies (counts and warnings once, cohort-wide); a chain stage admits it.
- A centroid reference is two-pass for every metric (and keeps a `fit` slot two-pass); weighted, cohort-wide.
- Matched BY LABEL: `PULSE_VECTOR_REFERENCE_MISMATCH` at predict; unbound population `PULSE_VECTOR_REFERENCE_UNKNOWN`; a failed compose fit (population or `fit`) nulls every row + `PULSE_FIT_UNAVAILABLE`. A record matches by member column; its lookup refuses at run time only (`PULSE_INDEX_*`, `PULSE_LOOKUP_*`).
- A null / profile-less key (`key_field` or `by`) or a domain violation (`hellinger`, `aitchison`) nulls the row and warns once per slot: `PULSE_VECTOR_REFERENCE_MISSING`, `PULSE_VECTOR_METRIC_DOMAIN`; strict does not escalate.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`vector-metrics`](vector-metrics.md), [`multivariate-design`](multivariate-design.md)
