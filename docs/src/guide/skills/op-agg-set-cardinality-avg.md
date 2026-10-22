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

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — `sum_cardinality / n`.

## Components

- `sum_cardinality` — Sum of popcounts
- `avg_cardinality` — Avg per row

- Margin: mean-reducible — needs per-cell `n` to combine

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `sum_cardinality`, `avg_cardinality`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- "Typical selections per respondent" — survey-friendly summary.
- Empty masks count toward `n` (lowering the average); pre-filter if you want only non-empty rows.
- No non-null mask (or Σw = 0) → `null` (NaN), never 0.
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
