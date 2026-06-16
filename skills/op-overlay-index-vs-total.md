---
name: op-overlay-index-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_TOTAL
description: Per-group streamable ratio index against the SERIES host's grand total (×100).
type: reference
applies_to: process, compose
examples_tags: [overlay, comparison, streaming-friendly]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref` | object | (empty) | Implicit-grand-total — leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

SERIES — grouped Process host. Family: implicit grand-total (no `Ref`). First streamable SERIES-host overlay with a streaming finalize hook. Sibling to `OVERLAY_SHARE_OF_TOTAL` (SERIES arm) — same accumulator, different scale.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = group_val / grand_total × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- `grand_total == 0` → NaN entries + ONE `PULSE_OVERLAY_REF_ZERO` warning per layer.
- Absent host group → `SeriesEntry` with unset `Statistic` and does NOT contribute to grand total.
- Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — one `float64` grand-total accumulator carried alongside per-group accumulators inside the streaming Process fold. Post-host finalize is the divide step.
- AGG_SUM semantics — counts post-filter rows, not pre-filter row count.

## See

- Skills: `overlay-system`, `op-overlay-share-of-total`, `op-overlay-zscore-vs-total`.
