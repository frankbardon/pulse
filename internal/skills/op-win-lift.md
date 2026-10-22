---
name: op-win-lift
description: Lift of a field over a base column — the field's share over the base's share, (x/Σx)/(b/Σb); cumulative gives a gains table's cumulative lift.
kind: operator
category: WIN
operator: WIN_LIFT
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
---

Row-level output; no `Response.Components`.

<!-- generated: use-when -->

## Params

`base` (field, required) — the comparison column; `cumulative` (bool, default `false`). Slots: `field` (required), `partition_by`, `order_by` (only with `cumulative: true`, then required), `frame` (refused).

## Inputs

`Field` and `params.base`: numeric (types in `## Contract`). On a decile table: defaults (field) over loans (base).

## Output

One `float64` per row on `Label` (default `WIN_LIFT_<field>`): `(x/Σx) / (b/Σb)` — exactly the field's share over the base's share, each total over its own present values. Cumulative: running share of field ÷ running share of base (`978/2149 ÷ 3600/18000`); `1` on the last row.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A null on either side or a zero base share reads `null`, never 0.
- Refusals are `PULSE_WINDOW_INVALID` on predict AND runtime: `frame`, `cumulative` without `order_by`, missing `field` / `base`.
- Not weightable (request `weight` → `PROCESSING_CONFIG`); buffered.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: `window-design`, `op-win-share`, `op-agg-auc`
