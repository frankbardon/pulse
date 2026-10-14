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

Compose-only parity overlay. Series-shape sibling of the cell z overlay. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels. A scalar scored aggregation (anything but `AGG_WELFORD`) REQUIRES `params.variance_target/_ref` and `params.sample_size_target/_ref` (or symmetric `variance` / `sample_size`), each > 0 — no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. They apply to EVERY group: p-values then describe the supplied values, not each group. `params.field` (string, optional, series slots) — OUTPUT LABEL of the aggregation to score on every slot (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each slot's `aggregations[0]`; rows key on group columns only; a slot lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN`.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

COMPOSE — SERIES grouped Process on both slots. **Parity overlay** — reads `{n, mean, variance}` from the scored `AGG_WELFORD`'s per-group `Response.Components` (weighted floor included; components disabled on that slot → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, reason `components_disabled`, predict and runtime), else the required `params`. Any other non-scalar scored aggregation is refused (`non_scalar`); crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin). Renders as a per-group strip.

## Output

SERIES — one `SeriesEntry` per host group key carrying the p-value on `Summary.Statistic`. One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted (both kinds): a row triple carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability), never the raw `n`, and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to the two-sample z-test on the same inputs — shares `normalTwoSidedP` with it and the cell z overlay.
- Missing reference row, or degenerate inputs (`se == 0`, `n < 2`) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.
- Unlike the streamable SERIES arm of the reference index / delta: inferential, buffered.

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`.
