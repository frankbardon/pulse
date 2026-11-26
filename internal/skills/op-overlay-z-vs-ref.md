---
name: op-overlay-z-vs-ref
kind: operator
category: OVERLAY
operator: OVERLAY_Z_VS_REF
description: Compose-host per-group two-sample z-test on the means against the reference slot's matching group (SERIES); reads each group's mean, variance and n from its components.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
---

Compose-only parity overlay: the two-means z per group, target slot against the reference slot; series sibling of the cell overlay. Shared contract: `overlay-reference-tests`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL to score on every slot (`label`, else `TYPE_field`) or an element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; missing on a slot → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `variance_target` | float | — | Target sample variance, > 0; required for a mean measure. |
| `variance_ref` | float | — | Reference sample variance; likewise. |
| `variance` | float | — | Both sides, when a side's own key is absent. |
| `sample_size_target` | float | — | Target sample size; likewise. |
| `sample_size_ref` | float | — | Reference sample size; likewise. |
| `sample_size` | float | — | Both sides, when a side's own key is absent. |

`scope` `group`; rows key on group columns. A mean measure needs the params for EVERY group and target (missing → `PULSE_OVERLAY_PARAM_MISSING`); other measures → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`measure_type`); mean vs moments → `PULSE_OVERLAY_SCHEMA_DIVERGENT`. Allowlist: `overlay-reference-tests`.

## Host shape

COMPOSE — SERIES host on both slots. Reads `{n, mean, variance}` from the per-group components of `AGG_WELFORD` or another moment-carrying measure (off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`), else `params`. Crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin).

## Output

SERIES — one entry per host group key, the p-value on `summary.statistic`; `summary.baseline` unset. One layer per target (`<name>__<target>` for 2+); cap `max_panel_targets`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A NaN value on either side → the entry keeps its key, NO `statistic` (absent, not `null`).
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`.
- Equals the two-sample z-test on the same inputs.
- Missing reference row or degenerate inputs → `PULSE_OVERLAY_REF_ZERO` (`ref_missing=true`), entry `null`.

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`.
