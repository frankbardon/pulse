---
name: item-fan-out
description: for_each on a window, feature, attribute, aggregation or matrix item, or a compose optimize block — one item fans out in place over parameter axes (grid or zip) into ordinary items, with the sweep placeholders, fan-out limits and predict parity.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ForEach, Window, Feature, Attribute, Aggregation, MatrixSpec, OptimizeSpec]
requires: [capability:for_each]
---

# Item fan-out (`for_each`)

`for_each` on ONE `windows`, `features`, `attributes` or `aggregations`<!-- feature: capability:matrices --> (or `matrices`)<!-- /feature --> item replaces it, in place, by one ordinary item per axis combination before anything else checks the request. Use it for near-identical items (an adstock grid of windows, an aggregation per channel, a matrix per invariance step). Plain items are untouched; the expanded list keeps declared order, the output order. Aggregations<!-- feature: capability:matrices --> and matrices<!-- /feature --> expand after the windows, so an aggregation may read `{{ch}}_lag`.

```jsonc
"windows": [{
  "for_each": {
    "axes": [{"name": "ch", "values": ["tv", "search"]},
             {"name": "n",  "values": [1, 2, 4]}],
    "mode": "grid"
  },
  "type": "<lag window>", "field": "{{ch}}_spend", "label": "{{ch}}_lag{{n}}",
  "order_by": [{"field": "week"}], "params": {"offset": {"$var": "n"}}
}]
```

Six items: `tv_lag1`, `tv_lag2`, `tv_lag4`, `search_lag1`, … in that order.

## Axes, modes, placeholders

- Axes and modes are the compose sweep's (`compose-sweeps`): unique identifier names, non-empty scalar values; `grid` (default) is the Cartesian product, first axis slowest; `zip` walks equal-length axes in lockstep.
- `{{axis}}` splices text inside a string such as `label` or `field` — never inside an expression (a formula `expression`, a params-borne `fit_on` or centroid filter, an extension's expression param): there it is `PULSE_FOR_EACH_EXPR_SPLICE`, refused before expansion. `{"$var": "axis"}` splices a whole value type-preserving, but only inside `params` (opaque JSON; a whole `fit_on` expression too); a typed key such as `order_by` cannot hold the marker.
<!-- feature: capability:params,capability:compose_sweep -->
- To vary a value a formula reads, use a compose sweep whose request sets `"params": {"x": {"$var": "x"}}` and read `params.x`.
<!-- /feature -->
- Every axis must be referenced in the item and every placeholder must name an axis. Not expr-lang: there is no arithmetic.

## The label-axis idiom (zip)

A label cannot compute `70` from `0.7`. Carry the label text as its own axis and zip it with the parameter axis, so each label pairs with exactly one value (an adstock grid of smoothing windows taking a `decay`):

```jsonc
"for_each": {"axes": [{"name": "d", "values": [10, 30, 50, 70]},
                      {"name": "decay", "values": [0.1, 0.3, 0.5, 0.7]}],
             "mode": "zip"},
"label": "tv_ad{{d}}", "params": {"decay": {"$var": "decay"}}
```

Under `grid` the same two axes would make 16 items, most mislabelled. To cross channels with zipped pairs, use one zip item per channel.

## Strictness and refusals

- Each rendered item decodes STRICTLY into its slot type: an unknown key is `PULSE_REQUEST_UNKNOWN_FIELD`, `details.path` from the item (`windows[0].bogus`), the combination in `index` / `values`.
- Every other fault is `PULSE_FOR_EACH_INVALID` with `details.field` (e.g. `windows[0].for_each.axes[1]`) and `details.reason`: the sweep's structural reasons, `axis_unreferenced`, `placeholder_unknown`, `placeholder_invalid`, `item_decode`.
- A `for_each` inside a compose `sweep` body is refused (`nested_in_sweep`): add the item's axes to the sweep instead. On a crosstab or banner cell it is `position_unsupported`.
- Each rendered matrix needs its own `name` (`"name": "{{step}}"`); a repeat is the usual duplicate-name refusal.

<!-- feature: capability:compose_optimize -->
## Optimize blocks

A Compose `optimize` block fans out too, right after the `base` merge: its `name` must carry an `{{axis}}` (`name_without_axis`); any other value varies whole as `{"$var": "axis"}` (a bound, `starts`, a constraint `expr`). The blocks it adds count toward `MaxExpandedItems`; predict lists `optimize[i]`, then each slot's `requests[i].…` fan-outs (`compose-optimize`).
<!-- /feature -->

## Limits, predict, hashing

- Checked before any item renders, over is `PULSE_LIMIT_EXCEEDED`: `Limits.MaxDerivedColumns` (default 1,000) counts plain plus expanded window, feature and attribute items; `Limits.MaxExpandedItems` (default 1,000) counts the aggregations<!-- feature: capability:matrices --> and matrices<!-- /feature --> a `for_each` adds (plain ones never count).
- Predict expands exactly as the runtime does and reports each fan-out under `for_each` `[{path, axes, mode, expanded_count}]`; its other answers judge the expanded items. Predict before running a large grid.
- Every request host expands: a process run, each compose slot, each chain stage, and a materialize run (every expanded item becomes a column). The echoed request carries the expanded items; its canonical hash equals the hand-written twin's.

## Templates, MCP and Explain

- In a stored request template (`request-templating`) the renderer leaves a `for_each` item's own `{{axis}}` and `{"$var": "axis"}` untouched for the expansion — no escape needed; an axis token inside an expression is refused at load (`PULSE_TEMPLATE_EXPR_SPLICE`). Template variables still substitute beside them and inside `for_each` itself, so `"values": {"$var": "decays"}` takes an axis list from a `list` variable. Axis names must be literal; a template variable named like an axis is refused at load (`PULSE_TEMPLATE_AXIS_COLLISION`).
- A cohort-bound MCP tool schema lets an item WITH `for_each` spell its field, type, `partition_by` / `order_by` keys and other enum-bound strings as `{{axis}}` placeholders; an item without one keeps the strict enums.
- Explain describes the expanded items (each naming its column or result) and states each fan-out.

## See

`compose-sweeps` (the same axes over whole requests) · `window-design` (window frames and ordering) · `feature-engineering` (multi-output features)
