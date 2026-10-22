---
name: op-agg-zscore
description: Standardized z-score aggregate — mean-centered, stddev-scaled summary.
kind: operator
category: AGG
operator: AGG_ZSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, comparison]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — the MEAN of the group's row z-scores, `0` by construction. The usable figures are the components; `zscore` there is the LAST row's `(target_value - pop_mean) / pop_stddev`. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `pop_mean` — Population mean (center)
- `pop_stddev` — Population stddev (scale)
- `target_value` — Value standardized
- `zscore` — Resolved score

<!-- generated: contract -->

## Gotchas

- Buffered path only.
- Empty group → `null` (NaN), scalar and components; zero stddev → scalar and `zscore` `null` (0/0), `pop_mean` / `pop_stddev` (0) / `target_value` defined.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `attribute-composition`, `response-components`
