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

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | string | (required) | Matched as `FILTER_INCLUDE` matches: category label, else a number (`date` days, `datetime` seconds, `packed_bool` `1`/`0`) |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any type EXCEPT `set_*` |

## Output

Scalar `float64` — rows equal to `value`, per group. = `FILTER_INCLUDE` + `AGG_COUNT`.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `match_count` | int | Rows equal to `value` (= scalar) |
| `share` | float64 | `match_count / n`; omitted when `n` is 0 |

- Mergeability: `Mergeable` (counters sum); ProcessChain admits it

## Gotchas

- Missing `value` → `PROCESSING_CONFIG` (runtime and predict). Modal count: `AGG_MODE_COUNT`.
- A value no row holds counts 0, not an error — check spelling.
- Nulls: not counted, not in `share`'s base.
- `set_*` rejected with `PROCESSING_CONFIG`; use `AGG_SET_FREQUENCY`.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
