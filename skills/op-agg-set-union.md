---
name: op-agg-set-union
description: Bitwise-OR a set field across rows; returns labels for every bit set in any contributing row.
kind: operator
category: AGG
operator: AGG_SET_UNION
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

Non-set fields rejected at construction time.

## Output

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the union mask.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mask_union` | uint64 | Bitwise OR across contributing rows |
| `popcount` | int | Bits set in `mask_union` |
| `labels` | `[]string` | Resolved dictionary labels |

- Mergeability: `Mergeable`; margin-summable for crosstab
- Streaming: per-chunk; OR is associative

## Gotchas

- Empty mask is a valid "no selection" — distinct from null.
- Union mask only honors bits seen in non-null rows.
- For per-bit row counts use `AGG_SET_FREQUENCY`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
