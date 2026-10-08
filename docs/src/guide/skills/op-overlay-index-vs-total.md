```yaml
name: op-overlay-index-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_TOTAL
description: Per-group streamable ratio index against the SERIES host's grand total (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, comparison, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100).

Questions it answers:

- How big is each region relative to the whole, on a 0 to 100 scale?
- What part of total sales, scaled to 100, does each channel make up?

Use something else:

- `OVERLAY_SHARE_OF_TOTAL` when you want the raw share (0 to 1).

## Params

`Scope` must be `group`. `Ref` (object, empty) — implicit-grand-total — leave empty. `Level`/`Within` must be `0`.

## Host shape

SERIES — grouped Process host. First streamable SERIES-host overlay with a streaming finalize hook. Sibling to the grand-total share (SERIES arm) — same accumulator, different scale.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = group_val / grand_total × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- `grand_total == 0` → NaN entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Absent host group → `SeriesEntry` with unset `Statistic` and does NOT contribute to grand total.
- Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — one `float64` grand-total accumulator carried alongside per-group accumulators inside the streaming Process fold. Post-host finalize is the divide step.
- Sum semantics — counts post-filter rows, not pre-filter row count.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-share-of-total`](op-overlay-share-of-total.md), [`op-overlay-zscore-vs-total`](op-overlay-zscore-vs-total.md).
