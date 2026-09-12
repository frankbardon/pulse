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

## Params

EXACTLY ONE of:

- `boundaries` (list[float]) — sorted ascending cutpoints; `v` lands in bucket `i` where `boundaries[i-1] < v <= boundaries[i]`.
- `quantiles` (int) — number of equal-population buckets.

## Inputs

`Field` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. No `decimal128`, no categorical, no `packed_bool`.

## Output

One `u32`-coded `f64` column at `Label` (default `BUCKET_<field>`) holding the bucket INDEX (0..N).

## Gotchas

- Neither OR both of `boundaries` / `quantiles` → `PROCESSING_CONFIG` at predict.
- Quantile mode is GLOBAL-PASS — sweeps the cohort for N-1 cutpoints before emitting. Buffered streaming still works (state survives `iter.Reset()`); file-backed iterators pay a second I/O. Explicit mode is PER-ROW and stateless.
- Boundaries are EXCLUSIVE on the lower edge — a value exactly on a cutpoint goes to the LOWER index (`SearchFloat64s` semantics).
- Null inputs emit a null bucket. `FILTER_RANGE` works directly on the integer code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-group-quantile` (aggregator-slot analogue), `op-feat-one-hot`
