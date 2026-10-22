```yaml
name: op-agg-set-union
description: Bitwise-OR a set field across rows; returns labels for every bit set in any contributing row.
kind: operator
category: AGG
operator: AGG_SET_UNION
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
```

## Use when

For a multi-select field, every option chosen by at least one row, over all rows or per group.

Questions it answers:

- Which brands were mentioned by anyone in this segment?
- Which features does at least one customer in each plan use?

Use something else:

- `AGG_SET_INTERSECTION` when you want the options every row chose.
- `AGG_SET_FREQUENCY` when you want how many rows chose each option.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the union mask.

## Components

- `mask_union` — Bitwise OR across contributing rows; 4 little-endian words, `words[0]` = bits 0-63
- `popcount` — Bits set in `mask_union`
- `labels` — Resolved dictionary labels

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `mask_union`, `popcount`, `labels`.

## Gotchas

- Empty mask is a valid "no selection" — distinct from null.
- Union mask only honors bits seen in non-null rows.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
