```yaml
name: overlay-pairwise
description: The contract shared by every intra-crosstab pairwise overlay kind — spec, the pair × opposite-axis p grid, the letters part, missing and degenerate pairs, weighted legs, proportion-cell versus moment-cell hosts and fusion.
type: guide
kind: design
applies_to: process, compose, predict
covers: [OVERLAY, pairwise, letters, pair_along_dim, payload.parts]
requires: [capability:crosstab]
```

# Pairwise overlays

A pairwise kind tests one host slot against another ALONG one axis of the SAME crosstab. This file is what every kind shares; the statistic, its formula and kind-only refusals live in each `op-overlay-pairwise-<name>` skill. Where each leg's n comes from: [`pairwise-n-sources`](pairwise-n-sources.md).

## Spec

- `Scope` (required) — `row` pairs row indices per column; `column` pairs column indices per row.
- `Ref` — intra-matrix, leave it empty: any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- `params.pair_along_dim` — restrict pairs to buckets agreeing on every pair-axis dim but this one; unset = every pair.
- `params.letters` — `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part.

## Host

A MATRIX crosstab (`Response.Crosstab.Matrix`) plus `Response.Components.Crosstab`: the family READS components and emits none. A components-disabled host (options or request) → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime.

## Output grid

MATRIX (`payload.shape = "matrix"`). The PAIR axis has one entry per evaluated `(i, j)` pair, keyed by the 2-tuple of the legs' labels; the OPPOSITE axis echoes the host's other axis. Each cell is the pair's two-sided p, absent when a leg is unreadable or the test degenerate. `row` scope → rows are pairs; `column` transposes.

The primary stays RAW p-values; direction and significance ride `letters` only when asked. Minimum-n flagging is the caller's job.

## The letters part

`payload.parts.letters` — MATRIX on the HOST coordinates. A cell of a compared pair-axis index holds `{letter, group, higher, above}`: its letter (`A`, `B`, … `Z`, `AA`, in axis order, restarting per comparison group — one group, or one per `pair_along_dim` bucket), the group, the letters of same-group indexes its figure beats (the sign of the kind's statistic) and the significant subset. Significant = raw p < `0.05` (or `alpha`, echoed as `summary.parameters.letters_alpha`); under a correction `significant_adjusted` (an explicit `alpha` compares `p_adjusted`). An index compared to nothing — the implicit Total column, a lone bucket member — has no cell. A multi-measure host letters each measure's rows.

## Missing and degenerate pairs

- An undefined (NaN) host cell is MISSING: its pairs skip as an absent cell's do (`PULSE_STATS_SKIP_EXTRACT_FAILED`).
- Degenerate pairs fold into ONE aggregated `PULSE_OVERLAY_REF_ZERO` per reason; the kind's skill names its degenerate cases.

## Proportion-cell kinds

The proportion kinds (pooled z, probit t) read a share and an n per leg.

- **`p_source` mismatch empties the layer.** A leg outside `[0,1]` (`cell_value` over a raw count or a 0..100 percentage) skips EVERY pair (`PULSE_OVERLAY_REF_ZERO`, reason `PULSE_STATS_SKIP_PROPORTION_INVALID`) — never clipped. A count cell needs `normalize` first (`row` for row scope).
- The distinct-KEY n modes are admitted only on a distinct-key cell aggregator and carry the slab partition refusal (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`): [`pairwise-n-sources`](pairwise-n-sources.md).
- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
- Buffered as a layer, but the HOST crosstab still FUSES on a mergeable cell aggregator (a weighted mean included, even over a per-element set axis).

## Moment-cell kinds

The two-means z and Welch t read the `{mean, variance, n}` triple per cell of an `AGG_WELFORD` cell.

- A cell type whose declared `ComponentSchema` (built-in or extension) lacks `mean` + `variance` (a sum or mode cell, …) → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, predict and runtime alike.
- The test reads the SAMPLE variance m2 / (n − 1): an unweighted population-variance cell (`AGG_VARIANCE`, `AGG_STDDEV`: m2 / n) is converted, so it scores as the Welford cell to the accumulators' last-bit rounding; an extension cell is read as is and must expose the sample variance.
- `n_source` / `p_source` would be silent no-ops (n comes from the triple): predict refuses both for EVERY mode (`PULSE_OVERLAY_PARAM_MISSING`); runtime leaves them inert. `n_within_depth` stays accepted and inert.
- Either leg with `n <= 1`, or an undefined (`null`) variance (n < 2 / Σw ≤ 1), skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`) — never scored off a 0 variance.
- The HOST is buffered too: the Welford cell is non-mergeable, so the fused-crosstab check rejects on the cell-aggregator arm.

## Weighted hosts

Each kind's `## Contract` says whether it is weighted: the probit t and the unweighted two-means z refuse any weight; the weighted two-means kind reads its own moment keys under `n_basis`. The rest, under both weight kinds and any weight source: the proportion z reads p̂ = the weighted cell share (Σw_success / Σw_base) and, with `n_source` omitted, n = the N* of that base, the cell's normalise denominator (`sum_weights` frequency, `n_eff` probability); `row_margin_n` / `column_margin_n` read a displayed margin's N*. The Welch t reads N* the same way, the variance from `m2` on w*, df on N*. Unweighted-count modes (and `cell_weight_sum` / `cell_value_weighted` under probability) are `PROCESSING_CONFIG`, predict AND runtime. `summary.parameters` adds `sum_weights` (+ `n_eff`).

## See

[`pairwise-n-sources`](pairwise-n-sources.md) · [`overlay-system`](overlay-system.md) · [`crosstab-guide`](crosstab-guide.md) · [`multiplicity-correction`](multiplicity-correction.md) · [`op-overlay-pairwise-prop-z`](op-overlay-pairwise-prop-z.md) · [`op-overlay-pairwise-probit-t`](op-overlay-pairwise-probit-t.md) · [`op-overlay-pairwise-two-means-z`](op-overlay-pairwise-two-means-z.md) · [`op-overlay-pairwise-welch-t`](op-overlay-pairwise-welch-t.md) · [`op-overlay-pairwise-weighted-two-means-z`](op-overlay-pairwise-weighted-two-means-z.md).
