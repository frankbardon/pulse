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

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Rich `map[string]int` — label→row count (weighted: `map[string]float64`, Σw). Scalar fallback = max single-label frequency.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

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
- Used as Crosstab Cell aggregator the result is a map-valued cell payload.
- Per-row a respondent may contribute to multiple bins — `total_label_observations` can exceed `n`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`crosstab-guide`](crosstab-guide.md), [`response-components`](response-components.md)
