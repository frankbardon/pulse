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

Compose-only parity overlay. Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. A scalar cell — any cell without mean + variance components (`AGG_WELFORD`<!-- feature: AGG_VARIANCE --> / `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> / `AGG_STDDEV`<!-- /feature --> with components on) — REQUIRES `params.variance_target/_ref` and `params.sample_size_target/_ref` (or symmetric `variance` / `sample_size`), each > 0 — no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. These ONE values per side apply to EVERY cell, so the p-values describe the supplied values (and the measure's units), not each cell's spread.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`), else the required `params`. Grouped slots → `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value via standard normal survival. One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted (both kinds, any weight source): a cell carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to the two-sample z-test on the same inputs (shared `normalTwoSidedP`).
- Differs from the cell t overlay only by distribution (normal vs Student t); same SE `sqrt(var_t/n_t + var_r/n_r)`.
- Legacy `processing.WelfordTriple` smuggle REMOVED v0.20.0; `MatrixCell.Value` holds the scalar mean.
- Buffered (inferential).

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`, `op-agg-welford`.
