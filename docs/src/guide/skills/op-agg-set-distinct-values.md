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

`Field` — admitted types: see `## Contract`.

## Output

Scalar `int64` — count of distinct exact masks.

## Components

- `mask_union` — Bitwise OR of every row's mask; 4 little-endian words, `words[0]` = bits 0-63
- `popcount` — Distinct labels observed
- `labels` — Resolved dictionary labels

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `mask_union`, `popcount`, `labels`.

## Gotchas

- Treats each unique combination as atomic — `{Visa, Amex}` ≠ `{Visa}`.
- Components carry the union, NOT a list of distinct masks (mask space can be huge).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
