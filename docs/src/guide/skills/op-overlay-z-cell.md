```yaml
name: op-overlay-z-cell
kind: operator
category: OVERLAY
operator: OVERLAY_Z_CELL
description: Compose-host per-cell two-sample z-test on the means against the reference slot's matching cell; parity overlay reading from CellComponents.
type: reference
applies_to: compose
examples_tags: [overlay, compose, hypothesis-test, z, byte-equal-test]
```

Compose-only parity overlay: the two-means z per crosstab cell, target slot against the reference slot. Shared contract: [`overlay-reference-tests`](overlay-reference-tests.md).

## Use when

Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?

Questions it answers:

- With large samples, which segment-by-question means moved since last wave?
- Which store-by-category average baskets differ between two high-traffic regions?

Use something else:

- `OVERLAY_T_CELL` when any cell is small; the t-based version is more honest there.

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

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided z-test p-value comparing the target's mean in that cell with the reference's, read from the normal curve; with small cells it comes out too small.
  - Caveat: The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted: legs read N* (`sum_weights` / `n_eff`), never the raw `n`; `Summary.Parameters` adds them.
- **Byte-equal** to the two-sample z-test on the same inputs (shared `normalTwoSidedP`).
- Differs from the cell t overlay only by distribution (normal vs Student t); same SE `sqrt(var_t/n_t + var_r/n_r)`.

## See

- Skills: [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-system`](overlay-system.md), [`response-components`](response-components.md), [`op-overlay-t-cell`](op-overlay-t-cell.md), [`op-overlay-z-vs-ref`](op-overlay-z-vs-ref.md), [`op-agg-welford`](op-agg-welford.md).
