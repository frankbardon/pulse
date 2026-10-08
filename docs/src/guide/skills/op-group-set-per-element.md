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

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`/`u16`/`u32`/`u64`/`u128`/`u256` |

## Output

One string key per set bit per row. Smart default for `set_*`. `GroupType.FansOut()` true — implements `MultiKeyStreamingGrouper`. Non-empty `Include` overrides dict-index emission order; zero-record labels dropped.

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
- Skills: [`grouper-design`](grouper-design.md), [`crosstab-guide`](crosstab-guide.md), [`op-group-set-value`](op-group-set-value.md)
