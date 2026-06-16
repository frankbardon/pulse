---
name: op-group-category
description: Partition records by exact field value; ideal for categorical fields.
kind: operator
category: GROUP
operator: GROUP_CATEGORY
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

None. `Group.Label` overrides output column name; `Group.Include []string` allow-lists bucket keys (label strings).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type |

## Output

One bucket per distinct value; bucket key = stringified value. Categorical fields resolve through the dictionary. Smart default grouper for `categorical_*` and `packed_bool` when `Type` is omitted.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `dict_size` | int | Distinct values observed across all buckets |
| `buckets` | []bucket | `{key, label, count}` per emission |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- `Group.Include` matches the post-dictionary label string.
- High-cardinality fields blow memory — pair with `FILTER_INCLUDE`.
- Use `GROUP_RANGE`/`GROUP_ROUNDED` for numeric binning.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `crosstab-guide`
