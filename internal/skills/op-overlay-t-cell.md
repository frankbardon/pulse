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

Compose-only parity overlay. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. Optional overrides `params.variance_target/_ref` (float, `1.0`), `params.sample_size_target/_ref` (int, `2`). Without `AGG_WELFORD` these ONE values per side apply to EVERY cell, so the p-values describe the supplied values (and the measure's units), not each cell's spread.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`), else the `params` triple.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value. One layer per target; `Baseline` unset.

## Gotchas

- Weighted (both kinds, any weight source): a cell carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to Welch's t-test on the same inputs — both read `{n, mean, variance}` via Welford and share `studentTTwoSidedP` and the df recurrence.
- Legacy `processing.WelfordTriple` smuggle REMOVED v0.20.0 — `MatrixCell.Value` carries the scalar mean.
- Canonical pairing: `AGG_WELFORD` + `OVERLAY_T_CELL`. Buffered (inferential).

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`, `op-agg-welford`.
