---
name: op-group-set-per-element
description: Fan each row into one bucket per selected label (multi-key). Cardinality multiplies with set popcount.
kind: operator
category: GROUP
operator: GROUP_SET_PER_ELEMENT
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
---

## Params

None. `Group.Label` renames the output column; `Group.Include` allow-lists fan-out labels (each matched independently) and fixes emission order.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`/`u16`/`u32`/`u64` |

## Output

One string key per set bit per row. Smart default for `set_*`. Implements `MultiKeyStreamingGrouper`. Non-empty `Include` overrides dict-index emission order; zero-record labels dropped.

## Components

Floor `{total_n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `total_label_observations` | int | Sum of `buckets[].count`; MAY exceed `total_n` — fan-out |
| `buckets` | []bucket | `{key, label, count, dict_index}` |

- Mergeability: `Mergeable`
- **Is** a fused-crosstab axis — admitted at any position, either or both axes.

## Gotchas

- `sum(buckets[].count) > total_n` is CORRECT.
- Empty-mask rows skipped; do NOT increment `n_null`. Null rows do.
- Crosstab margins are non-additive: a 3-label row counts 3× across row margins, once in the grand total. Both paths agree.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `crosstab-guide`, `op-group-set-value`
