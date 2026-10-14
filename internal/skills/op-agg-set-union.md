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

<!-- generated: use-when -->

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256`; any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |


## Output

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the union mask.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mask_union` | `[]uint64` | Bitwise OR across contributing rows; 4 little-endian words, `words[0]` = bits 0-63 |
| `popcount` | int | Bits set in `mask_union` |
| `labels` | `[]string` | Resolved dictionary labels |

- Mergeability: `Mergeable`; margin-summable for crosstab
- Streaming: per-chunk; OR is associative

## Gotchas

- Empty mask is a valid "no selection" — distinct from null.
- Union mask only honors bits seen in non-null rows.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
