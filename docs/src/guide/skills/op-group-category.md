```yaml
name: op-group-category
description: Partition records by exact field value; ideal for categorical fields.
kind: operator
category: GROUP
operator: GROUP_CATEGORY
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Splits the rows into one group per distinct value of a field, such as one group per region or per answer option.

Questions it answers:

- What is the average spend in each region?
- How many respondents chose each answer?

Use something else:

- `GROUP_RANGE` when the field is a number you want in bands, not one group per value.
- `GROUP_DATE` when the field is a date and you want months or quarters.

## Params

None. `Group.Label` overrides the output column name; `Group.Include []string` allow-lists bucket keys (label strings) and sets emission order.

## Inputs

`Field` — any cohort field type EXCEPT `set_*` (refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime).

## Output

One bucket per distinct value; key = stringified value (categoricals resolve through the dictionary). Smart default for `categorical_*` / `packed_bool` when `Type` omitted.

Non-empty `Include` is order-significant: buckets emit in `Include` order (`Data` + `Components.buckets`). Empty/absent keeps the prior alphabetical order — byte-identical.

## Components

Floor `{total_n, n_null}` + `dict_size` (int, distinct values observed) and `buckets` (`[]bucket` of `{key, label, count}` in emission order). `Mergeable`; `StreamableGrouper`, so eligible for fused crosstab.

## Gotchas

- `Include` matches the post-dictionary label; zero-record values drop, never emit empty.
- A categorical index outside its (widened) dictionary is corrupt data: `ENCODING_INVALID` (`details.reason` `categorical_index_out_of_range`) on every arm, never a numeric-key bucket.
- High-cardinality fields blow memory — filter to the values you need first.
- `set_*` rejected at build time with `PULSE_FIELD_TYPE_UNSUPPORTED` — a bitmask has no single scalar to bucket on at any rung.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`crosstab-guide`](crosstab-guide.md)
