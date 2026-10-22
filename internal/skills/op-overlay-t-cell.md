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

<!-- generated: use-when -->

## Params

`Scope` required, must be `cell`. `Reference` / `Targets` required slot labels. A scalar cell — any cell without mean + variance components (`AGG_WELFORD`<!-- feature: AGG_VARIANCE --> / `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> / `AGG_STDDEV`<!-- /feature --> with components on) — REQUIRES `params.variance_target/_ref` and `params.sample_size_target/_ref` (or symmetric `variance` / `sample_size`), each > 0 — no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. These ONE values per side apply to EVERY cell, so the p-values describe the supplied values (and the measure's units), not each cell's spread.


## Host shape

COMPOSE — MATRIX crosstab on both slots. **Parity overlay** — reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via `MetaAggregator`), else the required `params`. Grouped slots → `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`. The test reads the SAMPLE variance m2 / (n − 1): an unweighted<!-- feature: AGG_VARIANCE --> `AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV --> `AGG_STDDEV`<!-- /feature --> slot cell (population m2 / n) is converted, so it scores as `AGG_WELFORD` (to the accumulators' last-bit rounding); n < 2 → `null` cell + `PULSE_OVERLAY_REF_ZERO`, as Welford. An extension cell is read as is and must expose the sample variance.

## Output

MATRIX — `Cells[r][c].Value` = two-sided p-value. One layer per target; `Baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) target or reference cell is a MISSING pair: the cell stays absent, no warning, out of `summary.count`.
- Weighted (both kinds, any weight source): a cell carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read.
- **Byte-equal** to Welch's t-test on the same inputs — both read `{n, mean, variance}` via Welford and share `statdist.StudentTTwoSidedP` and the df recurrence.
- Legacy `processing.WelfordTriple` smuggle REMOVED v0.20.0 — `MatrixCell.Value` carries the scalar mean.
- Canonical pairing: `AGG_WELFORD` + `OVERLAY_T_CELL`. Buffered (inferential).

## See

- Skills: `overlay-system`, `response-components`, `op-overlay-z-cell`, `op-overlay-t-vs-ref`, `op-agg-welford`.
