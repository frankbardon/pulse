---
name: op-agg-constant
description: One typed constant in every row, group, crosstab cell and margin; fieldless.
kind: operator
category: AGG
operator: AGG_CONSTANT
type: reference
applies_to: process, compose, predict
examples_tags: [comparison, financial, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `type` | enum | (required) | One of the 22 field-type names. |
| `value` | any | — | Literal judged against `type`; `null` emits null. |
| `expr` | string | — | Scalar expression, evaluated once before the run. |

Give EXACTLY ONE of `value` / `expr` (`exactly_one_of: constant`). `value` is always a literal: integer in range (`u64` ≤ 2^53), finite number (`f32` rounds through float32), `true`/`false`, `"YYYY-MM-DD"`, RFC 3339 with offset (whole seconds), exact decimal string, a label, distinct member labels (`set_*`), 2–1024 numbers (`vec_*`). `expr` (`params.x`, `coef()`, `sweep_axis()`, `lookup()`, …) only for `u*`, `f*`, `packed_bool`, `date`, `datetime`; an unresolved one is listed in predict's `unresolved_scalars`.

## Inputs

None: no `field`, `vector` or `fields`; a `label` is required.

## Output

As existing aggregators write the type: a number (`date` epoch days, `datetime` epoch seconds, `packed_bool` 0/1), a decimal string at the literal's scale, a label, a label list, a number array. Same value in every group, cell and margin.

## Components

Floor only: `n` = records reaching the slot, `n_null` 0.

<!-- generated: contract -->

## Gotchas

- `PROCESSING_CONFIG` `details.reason`: `field_not_accepted`, `label_missing`, `type_invalid`, `value_and_expr`, `value_missing`, `value_invalid`, `expr_type_unsupported`, `expr_out_of_range`, `expr_*`.
- No weight: a slot `weight` is `weight_not_accepted`; a request weight skips it.
- `Return.precision` rounds it as a measure, never as a count.

## See

- `pulse_examples_search tags=[comparison, financial]`
- Skills: `aggregation-design`, `response-components`
