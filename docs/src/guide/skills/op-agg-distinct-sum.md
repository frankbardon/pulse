```yaml
name: op-agg-distinct-sum
description: Sum a value field once per distinct key.
kind: operator
category: AGG
operator: AGG_DISTINCT_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
```

## Use when

Total of a numeric field counting each key once, such as a respondent's weight summed once per respondent.

Questions it answers:

- What is the weighted base when each respondent appears on several rows?
- What is the total contract value when each contract repeats on every line item?

Use something else:

- `AGG_SUM` when every row is its own unit and should be added.

## Params

`distinct_by` (string, required) — field holding the distinct key. Absent or empty: `PROCESSING_CONFIG`, never defaulted to `Field`; unknown: `SERVICE_VALIDATION`.

## Inputs

`Field` — admitted types: see `## Contract`.

- `distinct_by` — any numeric-coded field, `categorical_*` included

## Output

Scalar `float64` — one value per key, summed.

## Components

- `sum` — Sum of the first value seen per key.
- `distinct_count` — Distinct keys that contributed.
- `MarginIndependent` margins: crosstabs fuse.

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `partial`.
- Components: `n`, `n_null`, `sum`, `distinct_count`.

## Gotchas

- FIRST VALUE WINS on a key with conflicting values; a merge keeps the receiver's, so it holds across shards too.
- The sum is binned: the same bits on every worker count and shard split when each key carries one value (a conflicting key keeps whichever value its arm saw first).
- Null in EITHER half contributes nothing and registers NO key; a later real row counts.
- A plain sum of a per-respondent weight multiplies it by row count; this gives the weighted base.
- Memory grows per key.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`crosstab-guide`](crosstab-guide.md)
