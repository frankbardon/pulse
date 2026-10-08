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

Compose-only parity overlay. Overlays decorate the host; no `Response.Components`.

## Use when

Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?

Questions it answers:

- With large samples, which segment-by-question means moved since last wave?
- Which store-by-category average baskets differ between two high-traffic regions?

Use something else:

- `OVERLAY_T_CELL` when any cell is small; the t-based version is more honest there.

## Params

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. Optional overrides `params.variance_target/_ref` (float, `1.0`), `params.sample_size_target/_ref` (int, `2`). Without `AGG_WELFORD` these ONE values per side apply to EVERY cell, so the p-values describe the supplied values (and the measure's units), not each cell's spread.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`), else the `params` triple.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value via standard normal survival. One layer per target; `Baseline` unset.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: Each cell holds the two-sided z-test p-value comparing the target's mean in that cell with the reference's, read from the normal curve; with small cells it comes out too small.
  - Caveat: The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

## Gotchas

- Weighted (both kinds, any weight source): a cell carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to the two-sample z-test on the same inputs (shared `normalTwoSidedP`).
- Differs from the cell t overlay only by distribution (normal vs Student t); same SE `sqrt(var_t/n_t + var_r/n_r)`.
- Legacy `processing.WelfordTriple` smuggle REMOVED v0.20.0; `MatrixCell.Value` holds the scalar mean.
- Buffered (inferential).

## See

- Skills: [`overlay-system`](overlay-system.md), [`response-components`](response-components.md), [`op-overlay-t-cell`](op-overlay-t-cell.md), [`op-overlay-z-vs-ref`](op-overlay-z-vs-ref.md), [`op-agg-welford`](op-agg-welford.md).
