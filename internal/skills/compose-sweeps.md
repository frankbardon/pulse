---
name: compose-sweeps
description: ComposedRequest.sweep — one request body with axis placeholders expands into many ordinary Compose slots (grid or zip), with a label pattern, per-slot overlays, limit counting and predict parity.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, SweepSpec, pulse_compose]
requires: [capability:compose_sweep]
---

# Compose sweeps

A **sweep** is one request body plus axes of values. Before any slot runs, Pulse expands it into ordinary Compose slots, appended after the explicit `requests`. Each expanded slot then runs exactly like a hand-written one (`compose-requests`). Use it when the same question repeats over a parameter grid; write plain `requests` when the slots differ in shape.

```jsonc
{
  "sweep": {
    "axes": [
      {"name": "region", "values": ["north", "south"]},
      {"name": "cut",    "values": [10, 20, 30]}
    ],
    "mode": "grid",
    "label": "{{region}}_{{cut}}",
    "request": {
      "cohort": {"filename": "sales.pulse"},
      "filterers": [{"type": "<value filter>", "field": "region", "values": [{"$var": "region"}]}],
      "aggregations": [{"field": "revenue", "label": "rev_{{cut}}"}]
    }
  }
}
```

## Axes and modes

- An axis is a unique identifier `name` plus a non-empty list of scalar `values` (string, number, boolean). Numbers keep the digits as written.
- `mode` `grid` (default) is the Cartesian product, first axis slowest: 2 x 3 values => 6 slots. `zip` walks the axes in lockstep and every axis must be the same length: 3 + 3 values => 3 slots.

## Placeholders

- `{{axis}}` inside a string splices the value as text. `{"$var": "axis"}` as a whole value keeps its JSON type (a number stays a number). A `$when` guard may name an axis. `{{` inside an object key is refused.
- Every axis must be referenced by the body, an authored `label` pattern or the overlays; a placeholder naming no axis is refused. Both ride `PULSE_SWEEP_INVALID` with `details.reason` (`axis_unreferenced`, `placeholder_unknown`, `placeholder_invalid`).
- This is NOT expr-lang and not the stored-template surface: substitution happens before the body is decoded.

## Labels

Omit `label` for the default: the `axis=value` pairs joined by `_`. An authored pattern may use any axis. The request body must not set its own `label` (`request_label_set`), and a pattern rendering empty is refused (`label_empty`). Labels must stay unique across explicit and sweep slots, or `PULSE_COMPOSE_LABEL_COLLISION`, before any slot runs. Overlays name sweep slots by these labels.

## Strict body

The substituted body decodes STRICTLY into a `Request`: an unknown key or a wrong-typed value is `PULSE_SWEEP_INVALID` `request_decode` (`details.unknown_field`), even where a plain compose body would ignore it. The failure is found at expansion, before any slot runs.

## Overlays

`sweep.overlays` is an array of compose overlay specs, substituted per expanded slot and appended after the request's own `overlays`. Use `{{axis}}` in `reference` / `targets` to point each copy at its slot label.

## With explicit requests, limits, predict

- `requests` and `sweep` combine: explicit slots first (indices unchanged), sweep slots after. A request with only a `sweep` is valid.
- `MaxComposeSlots` counts the EXPANDED total (explicit + sweep); an over-limit batch is `PULSE_LIMIT_EXCEEDED` before anything runs, serial and parallel alike.
- `pulse_predict` with `composed` (and `PredictCompose`) expands too: same limit, label and slot checks as runtime, plus a `sweep` summary `{axes[{name,count}], mode, expanded_count, labels}`. Predict a sweep before running it.
- `--echo-request` / `EchoRequest` echoes the expanded slots the service ran. MCP fills a missing `return` in the sweep body with the `standard` default, never overwriting an authored one.

## Rank

`sweep.rank {by, order, top}` orders the SWEEP slots (never explicit ones) by one number and reports `ranking: [{label, value, rank}]` on the ComposedResponse, 1-based, `order` `asc` (default) or `desc`, `top` keeps the first N (>= 1). `by` is a dot path into each slot's unshaped response: an object key, or on a list the element whose `name` or `label` equals the segment (never an index), ending on a number, e.g. `components.aggregations.rev.n` or `regressions.fit.residual_std_err`. Two matching elements, or a `name` and `label` that differ, are ambiguous.
- The batch is refused `PULSE_SWEEP_RANK_PATH` (`details.reason` `syntax`, `missing`, `not_number`, `ambiguous`) when `by` resolves in no slot, hits a non-number or is ambiguous; an empty segment is `syntax`.
- A null (NaN) value, or a path missing in this slot only, EXCLUDES that slot with a `PULSE_SWEEP_RANK_PATH` warning on its own response.
- `omitempty` figures vanish at exactly 0, so that slot reads as missing and is excluded (TODO #300).
- A ranked slot always computes the ranked part, even if its `return` excludes it. `ranking` sits outside the compose `return`: always emitted, never rounded.
- `--stream` ends with one `{"ranking":[...]}` line (even `[]`); rank-free sweeps emit none.

## See

`compose-requests` (slots, labels, overlay fold, parallel) · `overlay-system` (overlay hosts)<!-- feature: capability:templates --> · `request-templating` (the stored-template surface this is not)<!-- /feature -->.
