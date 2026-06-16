---
name: op-overlay-z-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_Z_VS_REF
description: Compose-host per-group two-sample z-test on the means against the reference slot's matching group (SERIES); parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
---

Compose-only parity overlay. Series-shape sibling of `OVERLAY_Z_CELL`. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Reference` | string | (required) | Reference slot label. |
| `Targets` | []string | (required) | Target slot labels. |
| `params.variance_target/_ref` | float | `1.0` | Optional. |
| `params.sample_size_target/_ref` | int | `2` | Optional. |

## Host shape

COMPOSE — SERIES grouped Process on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents` (matrix-shape arms via `AGG_WELFORD`); falls back to `params`-supplied triple. Renders as per-group inferential strip.

## Output

SERIES — one `SeriesEntry` per host group key, carrying p-value on `Summary.Statistic`. One layer per target. Layer `Baseline` unset.

## Gotchas

- **Byte-equal** to `TEST_Z_TWO_SAMPLE` over the same inputs — shares `standardNormalCDF` with `TEST_Z_TWO_SAMPLE` / `OVERLAY_Z_CELL`.
- Missing reference row → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`; entry NaN.
- Degenerate inputs (`se == 0`, `n < 2`) → NaN + same warning.
- Distinct from streamable SERIES arm of `OVERLAY_INDEX_VS_REF` / `OVERLAY_DELTA_VS_REF` — inferential family buffered by policy.

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`.
