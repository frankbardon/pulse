---
name: op-agg-mode-count
description: Modal count — how many rows hold the field's most common value; one float64 per output row.
kind: operator
category: AGG
operator: AGG_MODE_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, cardinality-analysis]
---

<!-- generated: use-when -->

## Params

Weighted: the largest per-value Σw (a float), its value the weighted mode.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — the modal count, NOT a per-value map. Per group under a grouper; grouped by category on the same field it equals each group's row count.

## Components

- `distinct_count` — Number of distinct values
- `mode_value` — Modal value (smallest wins a tie)
- `mode_count` — Row count of the modal value (= scalar; weighted: Σw)

<!-- generated: contract -->

## Gotchas

- Smart default for categorical_* and packed_bool. Memory grows with distinct values.
- Crosstab margin = modal count of the margin's own rows, never a sum of cells (class `independent`: fuses).

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
