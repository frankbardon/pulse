```yaml
name: op-overlay-t-cell
kind: operator
category: OVERLAY
operator: OVERLAY_T_CELL
description: Compose-host per-cell Welch t-test against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, welch, byte-equal-test]
```

Compose-only parity overlay: Welch's t per crosstab cell, target slot against the reference slot. Shared contract: [`overlay-reference-tests`](overlay-reference-tests.md).

## Use when

Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?

Questions it answers:

- Which segment-by-product cells changed in average rating since last wave?
- Where does the new site's average handling time differ from the old site's?

Use something else:

- `OVERLAY_Z_CELL` when every cell is large and you want the normal-curve version.

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

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided Welch t-test p-value comparing the target's mean in that cell with the reference's.
  - Caveat: Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`; `Summary.Parameters` adds them.
- **Byte-equal** to Welch's t-test on the same inputs (shared `statdist.StudentTTwoSidedP` and df recurrence).
- Canonical pairing: `AGG_WELFORD` + `OVERLAY_T_CELL`.

## See

- Skills: [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-system`](overlay-system.md), [`response-components`](response-components.md), [`op-overlay-z-cell`](op-overlay-z-cell.md), [`op-overlay-t-vs-ref`](op-overlay-t-vs-ref.md), [`op-agg-welford`](op-agg-welford.md).
