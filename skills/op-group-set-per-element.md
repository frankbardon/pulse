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

None. `Group.Label` overrides output column name; `Group.Include []string` allow-lists fan-out labels (each evaluated independently).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`/`set_u16`/`set_u32`/`set_u64` |

## Output

String key per selected label per row. Smart default for `set_*` fields. Implements `MultiKeyStreamingGrouper` — one key per bit set in the mask.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `total_label_observations` | int | Sum of `buckets[].count`; MAY exceed `total_n` — fan-out |
| `buckets` | []bucket | `{key, label, count, dict_index}` |

- Mergeability: `Mergeable`
- Streaming: `MultiKeyStreamingGrouper` — multi-key fan-out; NOT a fused-crosstab axis (use `GROUP_SET_VALUE` instead).

## Gotchas

- `sum(buckets[].count) > total_n` is CORRECT.
- Empty-mask rows skipped; does NOT increment `n_null`. Null rows do.
- `Group.Include` matches each fan-out label independently.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-set-value`
