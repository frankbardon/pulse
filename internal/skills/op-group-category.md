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

None. `Group.Label` overrides the output column name; `Group.Include []string` allow-lists bucket keys (label strings) and sets emission order.

## Inputs

`Field` — any cohort field type EXCEPT `set_*` (refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime).

## Output

One bucket per distinct value; key = stringified value (categoricals resolve through the dictionary). Smart default for `categorical_*` / `packed_bool` when `Type` omitted.

Non-empty `Include` is order-significant: buckets emit in `Include` order (`Data` + `Components.buckets`). Empty/absent keeps the prior alphabetical order — byte-identical.

## Components

Floor `{total_n, n_null}` + `dict_size` (int, distinct values observed) and `buckets` (`[]bucket` of `{key, label, count}` in emission order). `Mergeable`; `StreamableGrouper`, so eligible for fused crosstab.

<!-- generated: contract -->

## Gotchas

- `Include` matches the post-dictionary label; zero-record values drop, never emit empty.
- A categorical index outside its (widened) dictionary is corrupt data: `ENCODING_INVALID` (`details.reason` `categorical_index_out_of_range`) on every arm, never a numeric-key bucket.
- High-cardinality fields blow memory — filter to the values you need first.
- `set_*` rejected at build time with `PULSE_FIELD_TYPE_UNSUPPORTED` — a bitmask has no single scalar to bucket on at any rung.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `crosstab-guide`
