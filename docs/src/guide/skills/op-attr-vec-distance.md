```yaml
name: op-attr-vec-distance
description: Per-row distance to a target profile — plain, set or fitted (std_euclidean, mahalanobis) metrics across a vector's members, against a literal, label-map or registered reference.
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

- `metric` — distance only: `euclidean`, `sq_euclidean`, `manhattan`, `chebyshev`, `cosine` (1 − cos), `hellinger`, `aitchison`, `hamming`, `std_euclidean`, `mahalanobis`. Default from `kind` (measure → `std_euclidean`); none → `PULSE_VECTOR_METRIC_REQUIRED`. A similarity → `PULSE_VECTOR_METRIC_UNKNOWN`, `details.use` = `ATTR_PROFILE_SIMILARITY`.
- `reference` (required) — `[…]`, `{label: value}`, `{table, key}` or `{table, key_field}` (categorical or `u4`…`u64`, by base-10 form).
- `min_valid` — fewest complete pairs, default 1. `labels` — inline `fields` only.
- `fit_on`, `missing`, `fit` — fitted metrics only; `fit` reads a bound vector fit (a compose `fits` entry: [`compose-fits`](compose-fits.md)) by label instead of fitting (XOR `fit_on`; a regression's name `PULSE_MODEL_KIND_MISMATCH`).

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; exactly one of `vector` / `fields`.

## Output

One `f64` ≥ 0 per record; sums rescaled by p / p_observed.

## Components

Floor `{n, n_null}` + `n_reference_missing`, `n_domain_invalid`. `Mergeable`.

## Gotchas

- `std_euclidean` / `mahalanobis`: two-pass fit (listwise, weighted under a weight); N* ≤ p or singular → all null + warning.
- Matched BY LABEL: `PULSE_VECTOR_REFERENCE_MISMATCH` at predict.
- A null / profile-less key or a domain violation (`hellinger`, `aitchison`) nulls the row and warns once per slot: `PULSE_VECTOR_REFERENCE_MISSING`, `PULSE_VECTOR_METRIC_DOMAIN`; strict does not escalate.

## See

- `pulse_examples_search tags=[feature-engineering, multivariate]`
- Skills: [`vector-metrics`](vector-metrics.md), [`multivariate-design`](multivariate-design.md)
