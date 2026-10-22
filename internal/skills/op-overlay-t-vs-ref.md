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

Compose-only parity overlay: Welch's t per group, target slot against the reference slot; series sibling of the cell overlay. Shared contract: `overlay-reference-tests`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL to score on every slot (`label`, else `TYPE_field`) or an element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; missing on a slot → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `variance_target` | float | — | Target sample variance, > 0; required for a measure without mean + variance components. |
| `variance_ref` | float | — | Reference sample variance; likewise. |
| `variance` | float | — | Both sides, when a side's own key is absent. |
| `sample_size_target` | float | — | Target sample size; likewise. |
| `sample_size_ref` | float | — | Reference sample size; likewise. |
| `sample_size` | float | — | Both sides, when a side's own key is absent. |

`Scope` required, must be `group`. `Reference` / `Targets` required slot labels; rows key on group columns only. A scalar scored aggregation (anything but `AGG_WELFORD`) REQUIRES the variance and sample-size params, applied to EVERY group; missing → `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime.

## Host shape

COMPOSE — SERIES grouped Process on both slots. Reads `{n, mean, variance}` from the scored `AGG_WELFORD`'s per-group `Response.Components` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`), else the required `params`. Other non-scalar measures → `non_scalar`; crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin).

## Output

SERIES — one `SeriesEntry` per host group key, the p-value on `Summary.Statistic`; one layer per target, `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A NaN value on either side → the entry keeps its key, NO `statistic` (absent, not `null`).
- An engine `AGG_WELFORD` series is pinned to R `t.test` per group at 1e-10, weighted or not.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`.
- **Byte-equal** to Welch's t-test (shared `statdist.StudentTTwoSidedP` + df recurrence).
- Missing reference row or degenerate inputs → `PULSE_OVERLAY_REF_ZERO` (`ref_missing=true`), entry NaN.

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`.
