```yaml
name: op-agg-set-cardinality-avg
description: Average popcount per contributing row — typical number of selections.
kind: operator
category: AGG
operator: AGG_SET_CARDINALITY_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
```

## Use when

For a multi-select field, the average number of options chosen per row.

Questions it answers:

- How many brands does a respondent recognise on average?
- How many features does a typical customer have turned on?

Use something else:

- `AGG_SET_CARDINALITY_SUM` when you want the total number of selections.
- `AGG_SET_FREQUENCY` when you want which options were chosen.

## Params

None. Weight-aware (`"weight": null` opts out): Σw·popcount / Σw.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Scalar `float64` — `sum_cardinality / n`.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum_cardinality` | int | Sum of popcounts |
| `avg_cardinality` | float64 | Avg per row |

- Mergeability: `Mergeable`
- Margin: mean-reducible — needs per-cell `n` to combine
- Streaming: per-chunk

## Gotchas

- "Typical selections per respondent" — survey-friendly summary.
- Empty masks count toward `n` (lowering the average); pre-filter if you want only non-empty rows.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
