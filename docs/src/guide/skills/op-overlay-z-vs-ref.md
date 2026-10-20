```yaml
name: op-overlay-z-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_Z_VS_REF
description: Compose-host per-group two-sample z-test on the means against the reference slot's matching group (SERIES); parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
```

Compose-only parity overlay. Series-shape sibling of the cell z overlay. Overlays decorate the host; no `Response.Components`.

## Use when

Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?

Questions it answers:

- With large samples, which regions' average spend changed since last quarter?
- Which high-volume channels' average basket differs between the two periods?

Use something else:

- `OVERLAY_T_VS_REF` when any group is small; the t-based version is more honest there.

## Params

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels. A scalar scored aggregation (anything but `AGG_WELFORD`) REQUIRES `params.variance_target/_ref` and `params.sample_size_target/_ref` (or symmetric `variance` / `sample_size`), each > 0 — no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. They apply to EVERY group: p-values then describe the supplied values, not each group. `params.field` (string, optional, series slots) — OUTPUT LABEL of the aggregation to score on every slot (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = each slot's `aggregations[0]`; rows key on group columns only; a slot lacking it → `PULSE_OVERLAY_FIELD_UNKNOWN`.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

COMPOSE — SERIES grouped Process on both slots. **Parity overlay** — reads `{n, mean, variance}` from the scored `AGG_WELFORD`'s per-group `Response.Components` (weighted floor included; components disabled on that slot → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, reason `components_disabled`, predict and runtime), else the required `params`. Any other non-scalar scored aggregation is refused (`non_scalar`); crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin). Renders as a per-group strip.

## Output

SERIES — one `SeriesEntry` per host group key carrying the p-value on `Summary.Statistic`. One layer per target; `Baseline` unset.

## Reading the output

- `summary.statistic`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Despite the field name, each series entry's summary.statistic holds that group's two-sided z-test p-value, not a z value; Pulse does not emit z.
  - Caveat: It is read from the normal curve, so with small groups it comes out too small.

## Gotchas

- Series host: an undefined value (NaN — a mean over a group with no value, an unanswered element) on either side is a MISSING point, as on the request host — the entry keeps its key with NO `statistic` (absent, not `null`), no warning, out of `summary.count`.
- An engine `AGG_WELFORD` series is pinned to R `2·pnorm(-|z|)` on the Welch SE per group at 1e-10, unweighted and under both weight kinds.
- Weighted (both kinds): a row triple carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability), never the raw `n`, and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to the two-sample z-test on the same inputs — shares `normalTwoSidedP` with it and the cell z overlay.
- Missing reference row, or degenerate inputs (`se == 0`, `n < 2`) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.
- Unlike the streamable SERIES arm of the reference index / delta: inferential, buffered.

## See

- Skills: [`overlay-system`](overlay-system.md), [`response-components`](response-components.md), [`op-overlay-z-cell`](op-overlay-z-cell.md), [`op-overlay-t-vs-ref`](op-overlay-t-vs-ref.md).
