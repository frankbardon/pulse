---
name: op-win-formula
description: Row-local expression over a result row's columns and earlier windows' outputs — e.g. the ratio or gap of two share columns in a gains table.
kind: operator
category: WIN
operator: WIN_FORMULA
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
---

Row-level output; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `expression` | string | (required) | Expression returning a number over the row's columns. |

Slots: `label` (default `WIN_FORMULA`). `field`, `order_by`, `partition_by` and `frame` are refused: it reads only its own row.

## Inputs

Names in `expression` are the row's columns at this window's turn: group keys and aggregation labels on grouped rows, record fields on the record rows, plus the label of every window listed BEFORE it. `params.<name>` reads request params; embedder functions, `lookup()` and `range_label()` are available.

## Output

One `float64` per row on `Label`: `s_events / s_n` over two earlier share-window labels is the decile's lift.

<!-- generated: contract -->

## Gotchas

- A null in any column it reads makes the row `null` — `x ?? 0` never sees the nil. A nil or non-finite result (`1/0`) reads `null`.
- Refused on predict AND runtime alike, before any row: its own or a LATER window's label (`PULSE_WINDOW_INVALID` reason `forward_reference`), an unknown name (`SERVICE_VALIDATION`), a bool / string result, `ref()`, `coef()`, `total()` or bad syntax (`PULSE_FORMULA_COMPILE_ERROR`).
- Columns are untyped until a row runs: arithmetic on a label fails the run (`PROCESSING_RUNTIME`).
- Not weightable (request `weight` → `PROCESSING_CONFIG`); buffered.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: `window-design`, `op-win-share`, `op-win-lift`, `op-attr-formula`
