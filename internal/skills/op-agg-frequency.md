---
name: op-agg-frequency
description: Value count — how many non-null rows hold one chosen value (params.value); one float64 per output row.
kind: operator
category: AGG
operator: AGG_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | string | (required) | Matched like an include filter: category label, else a number (`date` days, `datetime` seconds, `packed_bool` `1`/`0`) |

Weighted: Σw of matches (a float); `share` = Σw_match/Σw.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — rows equal to `value`, per group.

## Components

- `match_count` — Rows equal to `value` (= scalar; weighted: Σw)
- `share` — `match_count / n`; omitted when `n` is 0

<!-- generated: contract -->

## Gotchas

- Missing `value` → `PROCESSING_CONFIG` (runtime and predict).
- A value no row holds counts 0, not an error.
- Nulls: not counted, not in `share`'s base.
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
