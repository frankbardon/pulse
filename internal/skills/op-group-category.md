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

<!-- generated: use-when -->

## Params

None. The key column is named after `field` (a group slot takes no `label`); `include` (label strings) allow-lists bucket keys and sets emission order.

## Inputs

`field` — admitted types: see `## Contract`.

## Output

One bucket per distinct value; key = stringified value (categoricals resolve through the dictionary; `±Inf` / NaN key `"+Inf"`, `"-Inf"`, `"NaN"` on every architecture). Smart default for `categorical_*` / `packed_bool` when `type` is omitted.

Non-empty `include` is order-significant: buckets emit in its order (`data` and components `buckets`). Absent: by value over a number or date-part field (`"2"` before `"10"`, negatives first), alphabetical over a categorical; a `sort` on the key column follows the same order.

## Components

- `dict_size` (int, distinct values observed), `buckets` (`{key, label, count}` in emission order).
- Eligible for the fused crosstab.

<!-- generated: contract -->

## Gotchas

- `include` matches the post-dictionary label; zero-record values drop, never emit empty.
- A categorical index outside its (widened) dictionary is corrupt data: `ENCODING_INVALID` (`details.reason` `categorical_index_out_of_range`) on every arm, never a numeric-key bucket.
- High-cardinality fields blow memory — filter to the values you need first.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `crosstab-guide`
