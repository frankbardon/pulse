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

Scalar `float64` — the modal count (rows holding the most common value), NOT a per-value map. Per group under a grouper; under `GROUP_CATEGORY` on the same field it equals each group's row count (the categorical smart default).

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `distinct_count` | int | Number of distinct values |
| `mode_value` | any | Modal value (smallest wins a tie, as `AGG_MODE`) |
| `mode_count` | int | Row count of the modal value (= scalar) |

- Mergeability: `Partial` — per-value count map merged exactly; ProcessChain admits it
- Streaming: per-chunk count maps merged bin-by-bin

## Gotchas

- Smart default for categorical_* and packed_bool fields.
- Per-value tallies: `GROUP_CATEGORY` + `AGG_COUNT`, or `FacetSchema`. The value itself: `AGG_MODE`.
- Memory grows with distinct values.
- `set_*` rejected with `PROCESSING_CONFIG`; use `AGG_SET_FREQUENCY`.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
