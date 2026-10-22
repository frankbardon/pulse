---
name: overlay-reference-tests
description: The contract shared by the inferential overlays that test a figure against a reference — the Compose parity tests (per cell, per group) and their explicit-moments rule, and the prior-period tests on an ordered series — host refusals, missing and degenerate entries, weighted legs.
type: guide
kind: design
applies_to: process, compose, predict
covers: [OVERLAY, parity overlay, prior-period test, variance_target, sample_size_target, Ref.Prior]
---

# Reference tests (overlays)

Two groups of inferential kinds score a host figure against a reference and emit a p-value per coordinate. What each group shares is here; the statistic and its R pin live in each `op-overlay-<name>` skill. Choosing a kind: `overlay-system`.

## Compose parity tests

The cell kinds (MATRIX, `Scope` `cell`) and the vs-reference kinds (SERIES, `Scope` `group`) compare each target slot against the `Reference` slot (a vs-reference layer renders as a per-group strip); `Reference` / `Targets` are required slot labels and there is one layer per target, `Baseline` unset. "Parity": each is byte-equal to the matching test on the same inputs — the t kinds to Welch's t-test (shared `statdist.StudentTTwoSidedP` and the df recurrence), the z kinds to the two-sample z-test (shared `normalTwoSidedP`); t and z differ only by the distribution, same SE `sqrt(var_t/n_t + var_r/n_r)`.

**Where the legs come from.** `{n, mean, variance}` per leg off the scored measure's components<!-- feature: AGG_WELFORD --> (`AGG_WELFORD` via `MetaAggregator`)<!-- /feature --> — a cell's `Response.Components.Crosstab.CellComponents[r][c]`, a group's per-group components (weighted floor included). Components disabled on that slot → `PULSE_OVERLAY_COMPONENTS_REQUIRED` (reason `components_disabled`), predict and runtime. `MatrixCell.Value` holds the scalar mean (the legacy triple smuggle was removed in v0.20.0).

**The explicit-moments rule.** A scalar scored measure — any cell or aggregation without mean + variance components — REQUIRES `params.variance_target` / `variance_ref` and `sample_size_target` / `sample_size_ref` (or the symmetric `variance` / `sample_size`, read when a side's own key is absent), each > 0, no default: missing → `PULSE_OVERLAY_PARAM_MISSING` (`side`, `params`), predict and runtime. These ONE values per side apply to EVERY cell or group, so the p-values describe the supplied values (and the measure's units), not each coordinate's spread.

**Sample variance.** The test reads m2 / (n − 1): an unweighted population-variance cell<!-- feature: AGG_VARIANCE --> (`AGG_VARIANCE`<!-- /feature --><!-- feature: AGG_STDDEV -->, `AGG_STDDEV`<!-- /feature -->: m2 / n) is converted, so it scores as the Welford cell to the accumulators' last-bit rounding; n < 2 → `null` + `PULSE_OVERLAY_REF_ZERO`. An extension cell is read as is and must expose the sample variance.

**Host refusals.** Cell kinds on grouped slots → `PULSE_OVERLAY_SLOT_NOT_CROSSTAB`; vs-reference kinds on crosstab slots → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (use the cell twin); any other non-scalar scored aggregation → refused, reason `non_scalar`.

**Missing and degenerate.** An undefined (NaN) target or reference figure is MISSING: a cell stays absent; a series entry keeps its key with NO `statistic` (absent, not `null`). No warning; out of `summary.count`. A missing reference row, or degenerate inputs (`se == 0`, `n < 2`) → `PULSE_OVERLAY_REF_ZERO` with `ref_missing=true`, entry NaN.

Buffered, unlike the streamable SERIES arm of the descriptive reference index / delta.

## Prior-period tests

The t, z and proportion-z prior kinds test each period against the one before it.

- **Spec.** `Scope` must be `group`. `Ref.Prior` (an empty `Ref` accepted; lag 1 only); other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `Level` / `Within` must be `0` (`PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`). `params.field` / `params.partition_by` as on the descriptive prior delta (unknown → `PULSE_OVERLAY_FIELD_UNKNOWN` / `PULSE_OVERLAY_PARTITION_INVALID`).
- **Hosts.** An ordered grouped Process (SERIES; partitions as the prior delta) or a crosstab with NO `columns` — its rows are the periods of the implicit Total column. A crosstab with columns, or several measures → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`reason: column_grouper`); a measure the kind cannot read → the same code with `measure_type`. Legs come from the measure's per-group (or per-cell) components: components disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`. Predict raises every refusal with the runtime's code.
- **Output.** SERIES — one `SeriesEntry` per period in host order: `Summary.Statistic` (this period minus the prior), `Summary.PValue` two-sided. Layer `Summary`: `count` (periods tested), `min` / `max` of the statistic.
- **Independence assumed.** The two periods must be separate samples; a panel (the same respondents both waves) makes the p-value wrong — Pulse cannot detect it.
- First period → `statistic` null, no `p_value` (null, never 0)<!-- feature: capability:multiplicity -->, not a multiplicity member<!-- /feature -->. A missing period keeps its key with no statistic and the carrier does not advance.
- A degenerate pair → NaN entry; one `PULSE_OVERLAY_REF_ZERO` warning per skip reason (`details.reason` a `PULSE_STATS_SKIP_*` code, `skipped`).

<!-- feature: capability:weighting -->
## Weighted legs

Both weight kinds, any weight source: a leg carrying `sum_weights` reads N* = `sum_weights` (frequency) or `n_eff` (probability) — never the raw `n` — and its variance from `m2` on w*; a share's p̂ is the weighted mean. `Summary.Parameters` adds `sum_weights` (+ `n_eff`) over the legs read (`weighting`).
<!-- /feature -->

## See

`overlay-system` · `response-components` · `compose-requests` · `op-overlay-t-cell` · `op-overlay-z-cell` · `op-overlay-t-vs-ref` · `op-overlay-z-vs-ref` · `op-overlay-t-vs-prior` · `op-overlay-z-vs-prior` · `op-overlay-prop-z-vs-prior` · `op-agg-welford`.
