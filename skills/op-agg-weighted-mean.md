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
| `weight_field` | string | (required) | Per-row weight field |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric except `decimal128` (incl. `date`, `datetime`, `packed_bool`, `u4`) |
| `weight_field` | numeric, same family |

## Output

Scalar `float64` — `sum(field * weight) / sum(weight)`.

## Components

Floor `{n, n_null}` plus (all float64; empty cell all 0):

| Key | Notes |
|---|---|
| `sum_weighted` | Σ field·w |
| `sum_weights` | Σw |
| `weighted_mean` | Σ field·w / Σw |
| `m2_weighted` | Σw(x − mean)² |
| `sum_weights_sq` | Σw² |
| `weighted_variance` | m2/(Σw−1); 0 if Σw ≤ 1 |
| `n_eff` | Kish (Σw)²/Σw² |

- Mergeability: `Mergeable` (weighted Chan-Welford)
- Streaming: per-chunk

## Gotchas

- Null/zero-weight rows skip the mean but STILL count in floor `n` (`n`/`n_null` track `Field`); size from `sum_weights`/`n_eff`.
- `decimal128` rejected.
- Unweighted: `AGG_AVERAGE`.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`, `op-overlay-pairwise-weighted-two-means-z`
