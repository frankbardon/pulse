---
name: op-overlay-index-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_REF
description: Compose-host ratio index of target slot value against the matching reference slot value (matrix or series).
type: reference
applies_to: compose
examples_tags: [overlay, compose, comparison]
---

Compose-only dual-shape. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell` (matrix host) or `group` (series host). |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels (one or more). |
| `params.scale` | float | `100` | Scale (set to `1` for raw ratio). |

## Host shape

COMPOSE dual-shape: MATRIX crosstab OR SERIES grouped Process on both reference + target. Schema-match + key-alignment + dict-prefix gates at the slot barrier. Ratio twin of `OVERLAY_DELTA_VS_REF`. First COMPOSE-only kind.

## Output

MATRIX (cell host) or SERIES (group host) — per-coordinate `(target / ref) × scale`. Layer `Baseline = scale` (default `100`).

## Gotchas

- Zero reference → NaN + ONE `PULSE_OVERLAY_REF_ZERO` warning per affected coord.
- Missing reference coordinate → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true` Detail flag.
- SERIES dispatch is fold-only — streamable per `OverlayStreamability`.
- MATRIX dispatch forced buffered by the slot barrier.
- `OverlayOptions.DictPrefixFast` enables byte-equal dictionary prefix probe.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-delta-vs-ref`, `op-overlay-panel-index-vs-ref`.
