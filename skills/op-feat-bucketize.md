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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `boundaries` | list[float] | — | Sorted ascending cutpoints; v lands in bucket i where `boundaries[i-1] < v <= boundaries[i]`. Mutually exclusive with `quantiles`. |
| `quantiles` | int | — | Number of equal-population buckets. Mutually exclusive with `boundaries`. |

EXACTLY ONE of `boundaries` / `quantiles` is required.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no categorical, no `packed_bool`) |

## Output

One `u32`-coded `f64` column written to `Label` (default `BUCKET_<field>`) containing the bucket INDEX (0..N) per row.

## Gotchas

- Specifying neither OR both of `boundaries` / `quantiles` → `PROCESSING_CONFIG` at predict.
- Quantile mode is GLOBAL-PASS — sweeps the entire cohort to derive N-1 cutpoints before emitting. Buffered streaming still works (state survives `iter.Reset()`); file-backed iterators pay a second I/O.
- Explicit mode is PER-ROW and stateless.
- Boundaries are EXCLUSIVE on the lower edge — value exactly on a cutpoint goes to the LOWER bucket index (`SearchFloat64s` semantics).
- Null inputs emit null bucket.
- Downstream filter on the bucket column: `FILTER_RANGE` works directly on the integer code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-group-quantile` (the aggregator-slot analogue), `op-feat-one-hot`
