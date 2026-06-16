---
name: op-agg-set-intersection
description: Bitwise-AND a set field across rows; returns labels for every bit set in every contributing row.
kind: operator
category: AGG
operator: AGG_SET_INTERSECTION
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

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the intersection mask.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mask_intersection` | uint64 | Bitwise AND across contributing rows |
| `popcount` | int | Bits set in `mask_intersection` |
| `labels` | `[]string` | Resolved dictionary labels |

- Mergeability: `Mergeable` per-chunk
- Margin reducibility: NOT margin-reducible — crosstab margins recompute from raw rows

## Gotchas

- AND across all rows ≠ AND across cells; do NOT pool margins.
- One row with all-zero bits → empty intersection for the group.
- For union semantics use `AGG_SET_UNION`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
