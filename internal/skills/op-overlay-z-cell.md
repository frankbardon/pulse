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

| Name | Type | Default | Description |
|---|---|---|---|
| `variance_target` | float | — | Target side's sample variance. Required (> 0) for a side whose cell carries no mean + variance components; no default. |
| `variance_ref` | float | — | Reference side's sample variance. Required (> 0) for a side whose cell carries no mean + variance components; no default. |
| `variance` | float | — | One sample variance for both sides, read when a side's own key is absent. |
| `sample_size_target` | float | — | Target side's sample size. Required (> 0) for a side whose cell carries no mean + variance components; no default. |
| `sample_size_ref` | float | — | Reference side's sample size. Required (> 0) for a side whose cell carries no mean + variance components; no default. |
| `sample_size` | float | — | One sample size for both sides, read when a side's own key is absent. |

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. A scalar cell — any cell without mean + variance components (`AGG_WELFORD`<!-- feature: AGG_VARIANCE --> / `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> / `AGG_STDDEV`<!-- /feature --> with components on) — REQUIRES `params.variance_target/_ref` and `params.sample_size_target/_ref` (or symmetric `variance` / `sample_size`), each > 0 — no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. These ONE values per side apply to EVERY cell, so the p-values describe the supplied values (and the measure's units), not each cell's spread.

## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`), else the required `params`. Grouped slots → `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`. The test reads the SAMPLE variance m2 / (n − 1): an unweighted<!-- feature: AGG_VARIANCE --> `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> `AGG_STDDEV`<!-- /feature --> slot cell (population m2 / n) is converted, so it scores as `AGG_WELFORD` (to the accumulators' last-bit rounding); n < 2 → `null` cell + `PULSE_OVERLAY_REF_ZERO`, as Welford. An extension cell is read as is and must expose the sample variance.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value via standard normal survival. One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted (both kinds, any weight source): a cell carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to the two-sample z-test on the same inputs (shared `normalTwoSidedP`).
- Differs from the cell t overlay only by distribution (normal vs Student t); same SE `sqrt(var_t/n_t + var_r/n_r)`.
- Legacy `processing.WelfordTriple` smuggle REMOVED v0.20.0; `MatrixCell.Value` holds the scalar mean.

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-t-cell`, `op-overlay-z-vs-ref`, `op-agg-welford`.
