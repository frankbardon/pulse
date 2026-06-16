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

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |

## Output

Rich `map[string]int` — label→row count. Scalar fallback = max single-label frequency.

## Components

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
- Skills: `aggregation-design`, `crosstab-guide`, `response-components`
