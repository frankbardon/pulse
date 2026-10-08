```yaml
name: op-agg-set-distinct-values
description: Count of distinct exact mask values seen; each combination is atomic.
kind: operator
category: AGG
operator: AGG_SET_DISTINCT_VALUES
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
```

## Use when

For a multi-select field, how many different combinations of options appear.

Questions it answers:

- How many different combinations of channels do customers use?
- How many different combinations of brands were picked (an empty answer counts as one)?

Use something else:

- `AGG_SET_FREQUENCY` when you want the count of each single option.
- `AGG_SET_UNION` when you want every option chosen by anyone.

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Scalar `int64` — count of distinct exact masks.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mask_union` | `[]uint64` | Bitwise OR of every row's mask; 4 little-endian words, `words[0]` = bits 0-63 |
| `popcount` | int | Distinct labels observed |
| `labels` | `[]string` | Resolved dictionary labels |

- Mergeability: `Mergeable` via union of seen-mask sets
- Streaming: per-chunk

## Gotchas

- Treats each unique combination as atomic — `{Visa, Amex}` ≠ `{Visa}`.
- Components carry the union, NOT a list of distinct masks (mask space can be huge).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
