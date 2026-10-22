---
name: op-overlay-z-cell
kind: operator
category: OVERLAY
operator: OVERLAY_Z_CELL
description: Compose-host per-cell two-sample z-test on the means against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
---

Compose-only parity overlay: the two-means z per crosstab cell, target slot against the reference slot. Shared contract: `overlay-reference-tests`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `variance_target` | float | — | Target sample variance, > 0; required for a measure without mean + variance components. |
| `variance_ref` | float | — | Reference sample variance; likewise. |
| `variance` | float | — | Both sides, when a side's own key is absent. |
| `sample_size_target` | float | — | Target sample size; likewise. |
| `sample_size_ref` | float | — | Reference sample size; likewise. |
| `sample_size` | float | — | Both sides, when a side's own key is absent. |

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. A scalar cell (no mean + variance components) REQUIRES the variance and sample-size params, applied to EVERY cell; missing → `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime.

## Host shape

COMPOSE — MATRIX crosstab on both slots. Reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (an `AGG_WELFORD` cell), else the required `params`. Grouped slots → `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`. A population-variance cell is converted to the sample variance; n < 2 → `null` cell + `PULSE_OVERLAY_REF_ZERO`.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value via standard normal survival. One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`; `Summary.Parameters` adds them.
- **Byte-equal** to the two-sample z-test on the same inputs (shared `normalTwoSidedP`).
- Differs from the cell t overlay only by distribution (normal vs Student t); same SE `sqrt(var_t/n_t + var_r/n_r)`.

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `response-components`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`, `op-agg-welford`.
