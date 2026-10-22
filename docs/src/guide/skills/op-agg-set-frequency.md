```yaml
name: op-agg-set-frequency
description: Per-bit row count — how many rows had each set label selected.
kind: operator
category: AGG
operator: AGG_SET_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
```

## Use when

For a multi-select field, how many rows chose each option, over all rows or per group.

Questions it answers:

- How many respondents selected each brand they have heard of?
- How many customers use each payment method?

Use something else:

- `GROUP_CATEGORY` when the field holds one value per row: group by it, then count.
- `AGG_SET_DISTINCT_VALUES` when you want how many different combinations were chosen.

## Params

None. Weight-aware (`"weight": null` opts out): Σw per member — map values become floats.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Rich `map[string]int` — label→row count (weighted: `map[string]float64`, Σw). Scalar fallback = max single-label frequency.

## Components

- `total_label_observations` — Sum of popcounts (label selections)
- `distinct_labels` — Distinct labels seen ≥ once
- `per_label_count` — Label → row count

- Margin: summable for crosstab

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `partial`.
- Components: `n`, `n_null`, `total_label_observations`, `distinct_labels`, `per_label_count`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Survey-friendly default ("respondents per issuer").
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.
- Used as Crosstab Cell aggregator the result is a map-valued cell payload.
- Per-row a respondent may contribute to multiple bins — `total_label_observations` can exceed `n`.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`crosstab-guide`](crosstab-guide.md), [`response-components`](response-components.md)
