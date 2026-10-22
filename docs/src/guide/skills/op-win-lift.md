```yaml
name: op-win-lift
description: Lift of a field over a base column — the field's share over the base's share, (x/Σx)/(b/Σb); cumulative gives a gains table's cumulative lift.
kind: operator
category: WIN
operator: WIN_LIFT
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
```

Row-level output; no `Response.Components`.

## Use when

Adds how many times its share of one column a row holds of another, such as a decile's share of defaults over its share of loans.

Questions it answers:

- How many times the average default rate do the riskiest deciles of a score capture?
- Which stores sell more than their share of floor space would predict?

Use something else:

- `WIN_SHARE` when you want one column's share of its own total.

## Params

`base` (field, required) — the comparison column; `cumulative` (bool, default `false`). Slots: `field` (required), `partition_by`, `order_by` (only with `cumulative: true`, then required), `frame` (refused).

## Inputs

`Field` and `params.base`: numeric (types in `## Contract`). On a decile table: defaults (field) over loans (base).

## Output

One `float64` per row on `Label` (default `WIN_LIFT_<field>`): `(x/Σx) / (b/Σb)` — exactly the field's share over the base's share, each total over its own present values. Cumulative: running share of field ÷ running share of base (`978/2149 ÷ 3600/18000`); `1` on the last row.

## Reading the output

- `value`: The field's share over the base's share: 1 means the row holds the same share of both, 2 twice its base share of the field. With cumulative, the running shares are compared, so 2.3 on the second decile says the top two deciles hold 2.3 times their share of loans in defaults.
  - Sign: positive means above 1: the row holds more of the field than its share of the base; negative means below 1: the row holds less of the field than its share of the base.
  - Caveat: A missing value on either side, or a base share of 0, reads null.

## Contract

- Types (`field`, `base`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- A null on either side or a zero base share reads `null`, never 0.
- Refusals are `PULSE_WINDOW_INVALID` on predict AND runtime: `frame`, `cumulative` without `order_by`, missing `field` / `base`.
- Not weightable (request `weight` → `PROCESSING_CONFIG`); buffered.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: [`window-design`](window-design.md), [`op-win-share`](op-win-share.md), [`op-agg-auc`](op-agg-auc.md)
