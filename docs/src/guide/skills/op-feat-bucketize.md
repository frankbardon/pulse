```yaml
name: op-feat-bucketize
description: Bin a numeric field into ordered buckets — explicit boundaries or N equal-population quantiles.
kind: operator
category: FEAT
operator: FEAT_BUCKETIZE
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, pre-filter, feature-pipeline]
```

Feature operators write derived fields before filters run; no `Response.Components`.

## Use when

Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number.

Questions it answers:

- Which income band is each customer in?
- Which spend decile (tenth of orders ranked by spend) does each order fall in?

Use something else:

- `GROUP_RANGE` when you want results grouped by fixed-width bands.

## Params

EXACTLY ONE of `boundaries` / `quantiles` (manifest `exactly_one_of: cutpoints`):

| Name | Type | Default | Description |
|---|---|---|---|
| `boundaries` | list | — | Sorted ascending cutpoints; `v` lands in bucket `i` where `boundaries[i-1] < v <= boundaries[i]`. |
| `quantiles` | int | — | Count of equal-population buckets. |

## Inputs

`field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). The type is judged before params.

## Output

One derived field at `label` (default `BUCKET_<field>`) holding the bucket INDEX, `0..N` (N = cutpoint count).

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Neither OR both of `boundaries` / `quantiles` → `PROCESSING_CONFIG` (predict too).
- Quantile mode is GLOBAL-PASS — sweeps the cohort for N-1 cutpoints before emitting. Streaming still works (state survives `iter.Reset()`; file-backed iterators pay a second I/O) — unless `field` is an earlier feature's output: then buffered. Explicit mode is PER-ROW, stateless.
- Boundaries are EXCLUSIVE on the lower edge — a value on a cutpoint goes to the LOWER index.
- Null inputs emit a null bucket; a range filter works on the integer code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`feature-engineering`](feature-engineering.md), [`op-group-quantile`](op-group-quantile.md) (aggregator-slot analogue), [`op-feat-one-hot`](op-feat-one-hot.md)
