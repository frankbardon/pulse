---
name: op-overlay-pairwise-probit-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROBIT_T
description: Intra-matrix axis-pairwise probit t-test (Φ⁻¹ transform of each leg's proportion, Student-t tail).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Intra-matrix pairwise along one axis of the SAME crosstab: `row` scope pairs row indices per column, `column` pairs column indices per row. Sibling of the pairwise proportion z — same proportion + n inputs, probit transform instead. Overlays decorate the host; no `Response.Components` (this family READS them).

<!-- generated: use-when -->

## Params

`Scope` (enum, required) — `row` or `column`. `Ref` (object, empty) — intra-matrix — leave empty. `params.pair_along_dim` (int, unset) — restrict pairs to same-bucket comparisons on the pair axis. `params.n_source` (omitted: the cell's base — the normalise denominator on a normalised host, its N* when weighted), `params.n_within_depth` and `params.p_source` (default `cell_value` on a normalised host, else `cell_value_pct`; omitted over an unnormalised mean / non-count cell → `PULSE_OVERLAY_PARAM_MISSING`) are the family-wide vocabulary, identical to `op-overlay-pairwise-prop-z`: `pairwise-n-sources`. `params.letters` (object, unset) — `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part.


## Host shape

MATRIX crosstab + `Response.Components.Crosstab`. Components-disabled host (options or request) → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values, layout identical to `op-overlay-pairwise-prop-z`. Per pair: `t = (Φ⁻¹(p_i) - Φ⁻¹(p_j)) / sqrt(1/n_i + 1/n_j)` against Student-t with `df = n_i + n_j - 2`, two-sided; proportions clipped to `[1e-10, 1-1e-10]` before Φ⁻¹. A design formula (market-research convention), pinned as is to R computing it at 1e-10: the `sqrt(1/n_i + 1/n_j)` standard error is not a probit share's real spread and no stock test computes it, so read the p as a convention, not a calibrated test. `payload.parts.letters` (with `params.letters`): per host cell `{letter, group, higher, above}` — `higher` from the sign of `t`, `above` its significant subset — as `op-overlay-pairwise-prop-z` describes.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) host cell is MISSING: its pairs skip as an absent cell's do (`PULSE_STATS_SKIP_EXTRACT_FAILED`).
- `df <= 0` (n_i + n_j <= 2) or a non-finite t skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- The primary stays RAW p-values; direction and significance ride `letters` only when asked; min-n is the embedder's job.
- `p_source` mismatch empties the layer: a leg outside `[0,1]` (`cell_value` over a raw count or a 0..100 percentage) skips every pair (`PULSE_OVERLAY_REF_ZERO`, reason `PULSE_STATS_SKIP_PROPORTION_INVALID`) — never clipped. Normalize a count cell first.
- **Distinct-KEY n modes carry the same admission and the same slab partition refusal as on prop-Z** — distinct-key cell aggregators only, `PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED` for a fan-out grouper at a summed-across depth, margin modes never gated. Full rules: `pairwise-n-sources`.
- Flagged buffered in `OverlayStreamability`; the HOST crosstab still FUSES on a mergeable cell aggregator.
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `pairwise-n-sources`, `crosstab-guide`, `op-overlay-pairwise-prop-z`, `op-test-t`.
