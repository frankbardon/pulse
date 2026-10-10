---
name: item-fan-out
description: for_each on a window, feature or attribute item — one item fans out in place over parameter axes (grid or zip) into ordinary items, with the sweep placeholders, a derived-column limit and predict parity.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ForEach, Window, Feature, Attribute]
requires: [capability:for_each]
---

# Item fan-out (`for_each`)

`for_each` on ONE `windows`, `features` or `attributes` item replaces it, in place, by one ordinary item per axis combination before anything else checks the request. Use it for a parameter grid of near-identical derived columns (an adstock grid of smoothing windows over several channels) instead of writing every item out. Plain items in the same list are untouched; the expanded list keeps declared order, which is the output column order.

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
- `{{axis}}` splices text inside any string — `label`, `field`, a formula `expression`. `{"$var": "axis"}` splices a whole value type-preserving, but only inside `params` (opaque JSON); a typed key such as `order_by` cannot hold the marker.
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

- Each rendered item decodes STRICTLY into its slot type: an unknown key is refused.
- Every fault is `PULSE_FOR_EACH_INVALID` with `details.field` (e.g. `windows[0].for_each.axes[1]`) and `details.reason`: the sweep's structural reasons, `axis_unreferenced`, `placeholder_unknown`, `placeholder_invalid`, `item_decode`.
- A `for_each` inside a compose `sweep` body is refused (`nested_in_sweep`): add the item's axes to the sweep instead.

## Limits, predict, hashing

- `Limits.MaxDerivedColumns` (default 1,000) counts every plain item plus every expanded one across the three lists, before any item renders; over it is `PULSE_LIMIT_EXCEEDED`.
- Predict expands exactly as the runtime does and reports each fan-out under `for_each` `[{path, axes, mode, expanded_count}]`; its other answers judge the expanded items. Predict before running a large grid.
- Every request host expands: a process run, each compose slot, each chain stage, and a materialize run (every expanded item becomes a column). The echoed request carries the expanded items; its canonical hash equals the hand-written twin's.

## Templates, MCP and Explain

- In a stored request template (`request-templating`) the renderer leaves a `for_each` item's own `{{axis}}` and `{"$var": "axis"}` untouched for the expansion — no escape needed. Template variables still substitute beside them and inside `for_each` itself, so `"values": {"$var": "decays"}` takes an axis list from a `list` variable. Axis names must be literal; a template variable named like an axis is refused at load (`PULSE_TEMPLATE_AXIS_COLLISION`).
- A cohort-bound MCP tool schema lets an item WITH `for_each` spell its field, type, `partition_by` / `order_by` keys and other enum-bound strings as `{{axis}}` placeholders; an item without one keeps the strict enums.
- Explain describes the expanded items (each naming its column) and states each fan-out.

## See

`compose-sweeps` (the same axes over whole requests) · `window-design` (window frames and ordering) · `feature-engineering` (multi-output features)
