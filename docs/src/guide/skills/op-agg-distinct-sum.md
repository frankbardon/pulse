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

| Param | Accepted field types |
|---|---|
| `Field` | numeric, no `decimal128` (incl. `u4`, `date`, `datetime`, `packed_bool`) |
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
- Null in EITHER half contributes nothing and registers NO key; a later real row counts.
- A plain sum of a per-respondent weight multiplies it by row count; this gives the weighted base.
- Memory grows per key.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`crosstab-guide`](crosstab-guide.md)
