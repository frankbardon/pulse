```yaml
name: op-group-set-per-element
description: Fan each row into one bucket per selected label (multi-key). Cardinality multiplies with set popcount.
kind: operator
category: GROUP
operator: GROUP_SET_PER_ELEMENT
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
```

## Use when

Splits a multi-select answer into one group per option, counting each row once in every option it picked.

Questions it answers:

- How many respondents picked each brand they are aware of?
- What is the average rating among users of each feature?

Use something else:

- `GROUP_SET_VALUE` when you want each exact combination of options as one group.
- `AGG_SET_FREQUENCY` when you only want the count per option, without other figures.

## Params

None. `Group.Label` renames the output column; `Group.Include` allow-lists fan-out labels and fixes emission order.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One string key per set bit per row. Smart default for `set_*`. `GroupType.FansOut()` true — implements `MultiKeyStreamingGrouper`. Non-empty `Include` overrides dict-index emission order; zero-record labels dropped.

## Components

- `total_label_observations` — Sum of `buckets[].count`; MAY exceed `total_n` — fan-out
- `buckets` — `{key, label, count, dict_index}`
- **Is** a fused-crosstab axis — admitted at any position, either or both axes.

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `total_n`, `n_null`, `total_label_observations`, `buckets`.

## Gotchas

- `sum(buckets[].count) > total_n` is CORRECT.
- Empty-mask rows skipped; do NOT increment `n_null`. Null rows do.
- A mask bit past the dictionary is corrupt data: `ENCODING_INVALID` (`set_member_out_of_range`), never silently dropped.
- Crosstab: a null or empty set reaches no cell, so no margin (column % = "% of those asked"). Margins are non-additive: 3 labels count 3× across row margins, once in the grand total.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`grouper-design`](grouper-design.md), [`crosstab-guide`](crosstab-guide.md), [`op-group-set-value`](op-group-set-value.md)
