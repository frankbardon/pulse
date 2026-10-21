---
name: op-agg-set-frequency
description: Per-bit row count — how many rows had each set label selected.
kind: operator
category: AGG
operator: AGG_SET_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
---

<!-- generated: use-when -->

## Params

None. Weight-aware (`"weight": null` opts out): Σw per member — map values become floats.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256`; any other type is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Rich `map[string]int` — label→row count (weighted: `map[string]float64`, Σw). Scalar fallback = max single-label frequency.

## Components

<!-- feature: capability:weighting -->Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.<!-- /feature -->

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `total_label_observations` | int | Sum of popcounts (label selections) |
| `distinct_labels` | int | Distinct labels seen ≥ once |
| `per_label_count` | `map[string]int` | Label → row count |

- Mergeability: `Partial` — bin-by-bin sum, but map allocation expensive; staged at terminal flush
- Margin: summable for crosstab

## Gotchas

- Survey-friendly default ("respondents per issuer").
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.
- Used as Crosstab Cell aggregator the result is a map-valued cell payload.
- Per-row a respondent may contribute to multiple bins — `total_label_observations` can exceed `n`.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `crosstab-guide`, `response-components`
