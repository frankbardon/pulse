---
name: op-agg-set-cardinality-sum
description: Sum of popcounts across contributing rows — total label selections seen.
kind: operator
category: AGG
operator: AGG_SET_CARDINALITY_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |

## Output

Scalar `int64` — total selections seen across rows.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum_cardinality` | int | Sum of popcounts |

- Mergeability: `Mergeable`; margin-summable
- Streaming: per-chunk popcount sum

## Gotchas

- Counts label selections, not rows — `sum_cardinality ≥ n` for any non-empty masks.
- Empty masks contribute 0 (and count toward `n`).
- For per-row average use `AGG_SET_CARDINALITY_AVG`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
