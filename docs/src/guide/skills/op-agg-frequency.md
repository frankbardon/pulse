```yaml
name: op-agg-frequency
description: Value count — how many non-null rows hold one chosen value (params.value); one float64 per output row.
kind: operator
category: AGG
operator: AGG_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, streaming-friendly]
```

## Use when

How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group.

Questions it answers:

- How many respondents answered Yes in each region?
- How many orders were returned each day?

Use something else:

- `AGG_MODE_COUNT` when you want how many rows share the field's most common value, whatever it is.
- `FILTER_INCLUDE` when you want rows holding any of several values: keep them, then count rows.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | string | (required) | Matched like an include filter: category label, else a number (`date` days, `datetime` seconds, `packed_bool` `1`/`0`) |

Weight-aware (`"weight": null` opts out): Σw of matches (a float); `share` = Σw_match/Σw.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any type EXCEPT `set_*` |

## Output

Scalar `float64` — rows equal to `value`, per group.

## Components

Weighted: floor adds `sum_weights`/`n_eff`/`n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `match_count` | int | Rows equal to `value` (= scalar; weighted: Σw) |
| `share` | float64 | `match_count / n`; omitted when `n` is 0 |

- Mergeability: `Mergeable` (counters sum); ProcessChain admits it

## Gotchas

- Missing `value` → `PROCESSING_CONFIG` (runtime and predict).
- A value no row holds counts 0, not an error.
- Nulls: not counted, not in `share`'s base.
- `set_*` rejected with `PROCESSING_CONFIG`.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
