```yaml
name: op-agg-mode
description: Most-frequent value of the field (ties broken by the smallest value).
kind: operator
category: AGG
operator: AGG_MODE
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
```

## Use when

Most common value of a field, over all rows or per group.

Questions it answers:

- What is the most common answer to the preferred-brand question?
- Which product is ordered most often in each store?

Use something else:

- `GROUP_CATEGORY` when you want the count of every value, not only the top one: group by the field, then count.
- `AGG_MEDIAN` when the field is numeric and you want its middle value.

## Params

Weight-aware (`"weight": null` opts out): the value with the largest Σw; `count` becomes that Σw (a float). Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any field type EXCEPT `set_*` and `decimal128`; any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Scalar `float64` — the modal value (categorical: its dictionary index). Per-group under a grouper. ProcessChain admits it: a later stage reads that index as a number, not a label.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `value` | any | Most-frequent value |
| `count` | int | Row count of the modal value (weighted: Σw) |
| `distinct_count` | int | Distinct values observed |
| `tie_count` | int | Values tied at the max count |

- Mergeability: `Partial` — map allocation
- Streaming: per-value counts merged at flush

## Gotchas

- Ties go to the SMALLEST value (or index), not the first seen; `tie_count > 1` flags a tie.
- High cardinality costs memory; pre-filter.
- No non-null value → `null` (NaN), never 0.
- `set_*` → `PULSE_FIELD_TYPE_UNSUPPORTED` (no modal scalar).
- Over `categorical_*` / `packed_bool` the value is a CODE: an overlay scoring it (any host; `params.field`, else `aggregations[0]`) is refused `PROCESSING_CONFIG`, reason `categorical_code` (+ `field`, `field_type`). So is a crosstab whose CELL is a code, under any MATRIX kind that reads cell values (`slot: "crosstab.cell"`; the moment pairwise kinds read none). Score a count or share instead. Code-ness survives ProcessChain: a later stage's column written by a code-emitting aggregation is still a code (`field_type` its origin type), so `AGG_MODE` over it, scored, is refused too.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
