```yaml
name: op-overlay-t-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_T_VS_REF
description: Compose-host per-group Welch t-test against the reference slot's matching group (SERIES); parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, welch, byte-equal-test]
```

Compose-only parity overlay. Series-shape sibling of the cell t overlay. Overlays decorate the host; no `Response.Components`.

## Use when

Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?

Questions it answers:

- Which regions' average spend changed between this quarter and last?
- In which age bands does the test cohort's mean score differ from the control's?

Use something else:

- `OVERLAY_Z_VS_REF` when every group is large and you want the normal-curve version.

## Params

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels. Optional `params.variance_target/_ref` (float, `1.0`), `params.sample_size_target/_ref` (int, `2`). Without `AGG_WELFORD` these apply to EVERY group: p-values then describe the supplied values, not each group.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

COMPOSE — SERIES grouped Process on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents` (matrix arms via `AGG_WELFORD`), else the `params` triple. Renders as a per-group strip.

## Output

SERIES — one `SeriesEntry` per host group key carrying the p-value on `Summary.Statistic`. One layer per target; `Baseline` unset.

## Reading the output

- `summary.statistic`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Despite the field name, each series entry's summary.statistic holds that group's two-sided Welch t-test p-value, not a t value; Pulse does not emit t.
  - Caveat: Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.

## Gotchas

- Weighted (both kinds): a row triple carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability), never the raw `n`, and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to Welch's t-test on the same inputs — shares `studentTTwoSidedP` + the df recurrence with it and the cell t overlay.
- Missing reference row, or degenerate inputs (`se == 0`, `n < 2`) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.
- Unlike the streamable SERIES arm of INDEX/DELTA_VS_REF: inferential, buffered by policy.

## See

- Skills: [`overlay-system`](overlay-system.md), [`response-components`](response-components.md), [`op-overlay-t-cell`](op-overlay-t-cell.md), [`op-overlay-z-vs-ref`](op-overlay-z-vs-ref.md).
