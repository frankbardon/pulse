---
name: op-feat-bucketize
description: Bin a numeric column into ordered buckets — explicit boundaries or N equal-population quantiles.
kind: operator
category: FEAT
operator: FEAT_BUCKETIZE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, pre-filter, feature-pipeline]
---

Feature operators emit derived columns; no `Response.Components`.

<!-- generated: use-when -->

## Params

EXACTLY ONE of `boundaries` / `quantiles` (manifest `exactly_one_of: cutpoints`; a Recommend draft names `boundaries`):

| Name | Type | Default | Description |
|---|---|---|---|
| `boundaries` | list | — | Sorted ascending cutpoints; `v` lands in bucket `i` where `boundaries[i-1] < v <= boundaries[i]`. |
| `quantiles` | int | — | Count of equal-population buckets. |

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). The type is judged before params.

## Output

One `u32`-coded `f64` column at `Label` (default `BUCKET_<field>`) holding the bucket INDEX (0..N).

<!-- generated: contract -->

## Gotchas

- Neither OR both of `boundaries` / `quantiles` → `SERVICE_VALIDATION` at predict (runtime `PROCESSING_CONFIG`).
- Quantile mode is GLOBAL-PASS — sweeps the cohort for N-1 cutpoints before emitting. Streaming still works (state survives `iter.Reset()`; file-backed iterators pay a second I/O) — unless `Field` is an earlier feature's column: then buffered. Explicit mode is PER-ROW, stateless.
- Boundaries are EXCLUSIVE on the lower edge — a value on a cutpoint goes to the LOWER index (`SearchFloat64s`).
- Null inputs emit a null bucket; a range filter works on the integer code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-group-quantile` (aggregator-slot analogue), `op-feat-one-hot`
