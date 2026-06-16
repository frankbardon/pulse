---
name: op-agg-set-distinct-values
description: Count of distinct exact mask values seen; each combination is atomic.
kind: operator
category: AGG
operator: AGG_SET_DISTINCT_VALUES
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |

## Output

Scalar `int64` — count of distinct exact masks.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mask_union` | uint64 | Bitwise OR of every contributing row's mask |
| `popcount` | int | Distinct labels observed |
| `labels` | `[]string` | Resolved dictionary labels |

- Mergeability: `Mergeable` via union of seen-mask sets
- Streaming: per-chunk

## Gotchas

- Treats each unique combination as atomic — `{Visa, Amex}` ≠ `{Visa}`.
- For per-label cardinality use `AGG_SET_FREQUENCY` or `AGG_DISTINCT_COUNT`.
- Components carry the union, NOT a list of distinct masks (mask space can be huge).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
