---
name: op-agg-distinct-sum
description: Sum a value field once per distinct key.
kind: operator
category: AGG
operator: AGG_DISTINCT_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

## Params

`distinct_by` (string, required) — field holding the distinct key. Absent or empty is REFUSED (`PROCESSING_CONFIG`), never defaulted to `Field`.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric, no `decimal128` (incl. `nullable_*`, `date`, `packed_bool`) |
| `distinct_by` | any numeric-coded field, `categorical_*` included |

## Output

Scalar `float64` — one value per key, summed.

## Components

Universal floor `{n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `sum` | float64 | Sum of the first value seen per key |
| `distinct_count` | int | Distinct keys that contributed |

- `Partial` merge (per-key map union); `MarginIndependent` margins — crosstabs fuse.

## Gotchas

- FIRST VALUE WINS on a key with conflicting values; a merge keeps the receiver's, so it holds across shards too.
- Null in EITHER half contributes nothing and registers NO key — a later real row still counts.
- `AGG_SUM` over a per-respondent weight multiplies it by row count; this gives the weighted base.
- Memory grows with distinct keys.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `crosstab-guide`
