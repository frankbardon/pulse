```yaml
name: op-agg-set-cardinality-sum
description: Sum of popcounts across contributing rows — total label selections seen.
kind: operator
category: AGG
operator: AGG_SET_CARDINALITY_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
```

## Use when

For a multi-select field, the total number of options chosen across all rows.

Questions it answers:

- How many brand mentions did the survey collect in total?
- How many add-ons were sold across all orders?

Use something else:

- `AGG_SET_CARDINALITY_AVG` when you want the number per row.
- `AGG_SET_FREQUENCY` when you want the count for each option.

## Params

None. Weight-aware (`"weight": null` opts out): Σw·popcount (a float).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `int64` — total selections seen across rows (weighted: `float64`).

## Components

- `sum_cardinality` — Sum of popcounts

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `sum_cardinality`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Counts label selections, not rows — `sum_cardinality ≥ n` for any non-empty masks.
- Empty masks contribute 0 (and count toward `n`).
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
