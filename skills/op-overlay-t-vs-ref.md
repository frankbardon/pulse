---
name: op-overlay-t-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_T_VS_REF
description: Compose-host per-group Welch t-test against the reference slot's matching group (SERIES); parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, welch, byte-equal-test]
---

Compose-only parity overlay. Series-shape sibling of `OVERLAY_T_CELL`. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels. Optional `params.variance_target/_ref` (float, `1.0`), `params.sample_size_target/_ref` (int, `2`).

## Host shape

COMPOSE — SERIES grouped Process on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents` (matrix arms via `AGG_WELFORD`), else the `params` triple. Renders as a per-group strip.

## Output

SERIES — one `SeriesEntry` per host group key carrying the p-value on `Summary.Statistic`. One layer per target; `Baseline` unset.

## Gotchas

- **Byte-equal** to `TEST_WELCH` on the same inputs — shares `studentTTwoSidedP` + the df recurrence with `TEST_T` / `OVERLAY_T_CELL`.
- Missing reference row, or degenerate inputs (`se == 0`, `n < 2`) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.
- Unlike the streamable SERIES arm of INDEX/DELTA_VS_REF: inferential, buffered by policy.

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`.
