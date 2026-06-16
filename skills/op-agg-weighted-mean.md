---
name: op-agg-weighted-mean
description: Weighted arithmetic mean — sum(field * weight) / sum(weight).
kind: operator
category: AGG
operator: AGG_WEIGHTED_MEAN
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, comparison]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `weight_field` | string | (required) | Schema field whose value is the per-row weight. Null weight or weight==0 rows skipped. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |
| `weight_field` | numeric, same family as `Field` |

## Output

Scalar `float64` — `sum(field * weight) / sum(weight)`.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum_weighted` | float64 | Running sum of `field * weight` |
| `sum_weights` | float64 | Running sum of weights |
| `weighted_mean` | float64 | Resolved ratio |

- Mergeability: `Mergeable` (weighted Chan-Welford)
- Streaming: per-chunk

## Gotchas

- Rows with null weight OR weight==0 are SKIPPED (do not count toward `n`).
- `decimal128` rejected.
- For unweighted mean use `AGG_AVERAGE`.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
