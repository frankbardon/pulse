```yaml
name: op-feat-bucketize
description: Bin a numeric column into ordered buckets — explicit boundaries or N equal-population quantiles.
kind: operator
category: FEAT
operator: FEAT_BUCKETIZE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, pre-filter, feature-pipeline]
```

Feature operators emit derived columns; no `Response.Components`.

## Use when

Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number.

Questions it answers:

- Which income band is each customer in?
- Which spend decile (tenth of orders ranked by spend) does each order fall in?

Use something else:

- `GROUP_RANGE` when you want results grouped by fixed-width bands.

## Params

EXACTLY ONE of:

- `boundaries` (list[float]) — sorted ascending cutpoints; `v` lands in bucket `i` where `boundaries[i-1] < v <= boundaries[i]`.
- `quantiles` (int) — count of equal-population buckets.

## Inputs

`Field` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. No `decimal128`, no categorical, no `packed_bool`.

## Output

One `u32`-coded `f64` column at `Label` (default `BUCKET_<field>`) holding the bucket INDEX (0..N).

## Gotchas

- Neither OR both of `boundaries` / `quantiles` → `PROCESSING_CONFIG` at predict.
- Quantile mode is GLOBAL-PASS — sweeps the cohort for N-1 cutpoints before emitting. Streaming still works (state survives `iter.Reset()`); file-backed iterators pay a second I/O. Explicit mode is PER-ROW, stateless.
- Boundaries are EXCLUSIVE on the lower edge — a value on a cutpoint goes to the LOWER index (`SearchFloat64s`).
- Null inputs emit a null bucket; a range filter works on the integer code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-group-quantile`](op-group-quantile.md) (aggregator-slot analogue), [`op-feat-one-hot`](op-feat-one-hot.md)
