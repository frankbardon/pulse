```yaml
name: op-win-formula
description: Row-local expression over a result row's columns and earlier windows' outputs — e.g. the ratio or gap of two share columns in a gains table.
kind: operator
category: WIN
operator: WIN_FORMULA
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
```

Row-level output; no `Response.Components`.

## Use when

Adds a number to every result row computed from that row's columns and earlier windows, such as one share divided by another.

Questions it answers:

- What is each decile's share of defaults minus its share of loans?
- What is each region's revenue per order, next to its share of the total?

Use something else:

- `WIN_LIFT` when you want one column's share over another's share, the lift of a gains table.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `expression` | string | (required) | Expression returning a number over the row's columns. |

Slots: `label` (default `WIN_FORMULA`). `field`, `order_by`, `partition_by` and `frame` are refused: it reads only its own row.

## Inputs

Names in `expression` are the row's columns at this window's turn (group keys and aggregation labels on grouped rows, record fields on record rows) plus every EARLIER window's label. `params.<name>` reads request params; embedder functions, `lookup()` and `range_label()` work.

## Output

One `float64` per row on `Label`: `s_events / s_n` over two earlier share labels is the decile's lift.

## Contract

- Types: reads no field; naming one is `PULSE_WINDOW_INVALID`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- A null input makes the row `null` (`x ?? 0` never sees the nil); so does a nil or non-finite result (`1/0`).
- Refused at predict and runtime alike, before any row: its own or a LATER window's label (`PULSE_WINDOW_INVALID` reason `forward_reference`), an unknown name (`SERVICE_VALIDATION`), a bool / string result, `ref()`, `coef()`, `total()` or bad syntax (`PULSE_FORMULA_COMPILE_ERROR`).
- Record fields are typed (a categorical is a string): `region * 2` is refused before any row; on grouped rows it fails the run (`PROCESSING_RUNTIME`).
- Not weightable (request `weight` → `PROCESSING_CONFIG`); buffered.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: [`window-design`](window-design.md), [`op-win-share`](op-win-share.md), [`op-win-lift`](op-win-lift.md), [`op-attr-formula`](op-attr-formula.md)
