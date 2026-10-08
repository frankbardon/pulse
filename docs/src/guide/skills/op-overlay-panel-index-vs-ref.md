```yaml
name: op-overlay-panel-index-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_PANEL_INDEX_VS_REF
description: Compose-host multi-reference index — indexes every target slot against a shared reference; emits one layer per target.
type: reference
applies_to: compose
examples_tags: [overlay, compose, comparison]
```

Compose-only multi-reference. Overlays decorate the host; no `Response.Components`.

## Use when

Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100).

Questions it answers:

- How do the last four waves each compare with the benchmark wave?
- How does each market compare with the reference market, cell by cell?

Use something else:

- `OVERLAY_INDEX_VS_REF` when there is only one target.

## Params

`Scope` required — `cell` (matrix host) or `group` (series host). `Reference` = shared reference slot. `Targets` — one layer per target. `OverlayOptions.MaxPanelTargets` int, default `16`, caps `len(Targets)`.

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES on reference and every target. Schema-match + key-alignment + dict-prefix gates per (ref, target). Sibling to the proportion-z panel.

## Output

ONE layer per target, `layers[i].Name = "<spec.Name>__<spec.Targets[i]>"`. Payload shape mirrors the reference slot's. Byte-equal per layer to the two-slot reference index on each `(reference, target[i])`.

## Gotchas

- `len(Targets) > MaxPanelTargets` → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.
- Empty `spec.Name` → label becomes `OVERLAY_PANEL_INDEX_VS_REF__<target_label>`.
- Streamable: SERIES fold-only; MATRIX forced buffered by the slot barrier.
- Shared coord space is enforced before dispatch.
- Layer slice order matches `spec.Targets`, stable across re-runs.
- Weighted slots → weighted figures (reads the slot payloads).

## See

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-index-vs-ref`](op-overlay-index-vs-ref.md), [`op-overlay-prop-z-panel`](op-overlay-prop-z-panel.md).
