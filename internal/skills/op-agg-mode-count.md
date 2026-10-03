---
name: op-agg-mode-count
description: Modal count — how many rows hold the field's most common value; one float64 per output row.
kind: operator
category: AGG
operator: AGG_MODE_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, cardinality-analysis]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any type EXCEPT `set_*` |

## Output

Scalar `float64` — the modal count, NOT a per-value map. Per group under a grouper; grouped by category on the same field it equals each group's row count.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `distinct_count` | int | Number of distinct values |
| `mode_value` | any | Modal value (smallest wins a tie) |
| `mode_count` | int | Row count of the modal value (= scalar) |

- Mergeability: `Partial` — per-value count map merged exactly; ProcessChain admits it
- Streaming: count maps merged at flush

## Gotchas

- Smart default for categorical_* and packed_bool. Memory grows with distinct values.
- Crosstab margin = modal count of the margin's own rows, never a sum of cells (class `independent`: fuses).
- `set_*` → `PROCESSING_CONFIG`.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
