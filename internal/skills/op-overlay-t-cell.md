---
name: op-overlay-t-cell
kind: operator
category: OVERLAY
operator: OVERLAY_T_CELL
description: Compose-host per-cell Welch t-test against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, welch, byte-equal-test]
---

Compose-only parity overlay: Welch's t per crosstab cell, target slot against the reference slot. Shared contract: `overlay-reference-tests`.

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

MATRIX — `Cells[r][c].Value` = two-sided p-value (Student t, Welch–Satterthwaite df). One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`; `Summary.Parameters` adds them.
- **Byte-equal** to Welch's t-test on the same inputs (shared `statdist.StudentTTwoSidedP` and df recurrence).
- Canonical pairing: `AGG_WELFORD` + `OVERLAY_T_CELL`.

## See

- Skills: `overlay-reference-tests`, `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`, `op-agg-welford`.
