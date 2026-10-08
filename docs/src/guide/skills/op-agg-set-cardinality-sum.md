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

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Scalar `int64` — total selections seen across rows (weighted: `float64`).

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum_cardinality` | int | Sum of popcounts |

- Mergeability: `Mergeable`; margin-summable
- Streaming: per-chunk popcount sum

## Gotchas

- Counts label selections, not rows — `sum_cardinality ≥ n` for any non-empty masks.
- Empty masks contribute 0 (and count toward `n`).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`cohort-schema-design`](cohort-schema-design.md), [`response-components`](response-components.md)
