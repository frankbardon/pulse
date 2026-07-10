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

None. `Group.Label` overrides the output column name; `Group.Include []string` allow-lists bucket keys (label strings) and sets emission order.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type |

## Output

One bucket per distinct value; key = stringified value (categorical fields resolve through the dictionary). Smart default for `categorical_*` / `packed_bool` when `Type` omitted.

Non-empty `Include` is order-significant: buckets emit in `Include` order (`Data` + `Components.buckets`). Empty/absent `Include` keeps prior alphabetical order — byte-identical.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `dict_size` | int | Distinct values observed |
| `buckets` | []bucket | `{key, label, count}` in emission order |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- `Include` matches the post-dictionary label; zero-record values are dropped, not emitted empty.
- High-cardinality fields blow memory — pair with `FILTER_INCLUDE`.
- `GROUP_RANGE`/`GROUP_ROUNDED` for numeric binning.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `crosstab-guide`
