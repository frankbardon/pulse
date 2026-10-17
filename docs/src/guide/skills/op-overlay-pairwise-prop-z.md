```yaml
name: op-overlay-pairwise-prop-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROP_Z
description: Intra-matrix axis-pairwise pooled-SE two-proportion z-test (row-vs-row or col-vs-col within one crosstab).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
```

One host-matrix slot against another ALONG one axis of the SAME crosstab — the per-Request counterpart to Compose's proportion-z panel. Overlays decorate the host; no `Response.Components` (this family READS them).

## Use when

Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share?

Questions it answers:

- Which age bands differ from each other in the share choosing each brand?
- Which pairs of regions differ in their share of late deliveries?

Use something else:

- `OVERLAY_PAIRWISE_WELCH_T` when the cells are averages of a numeric measure rather than shares.

## Params

`Scope` (enum, required) — `row` (pair rows per column) or `column` (pair columns per row). `Ref` (object, empty) — intra-matrix; any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.pair_along_dim` (int, unset) — restrict pairs to buckets agreeing on all pair-axis dims but this one; unset = every pair. `params.n_source` (omitted: the cell's base — on a normalised host the denominator its share is of, as a count or, weighted, its N*), `params.n_within_depth` (default `0`) and `params.p_source` (default `cell_value` on a normalised host, else `cell_value_pct` — omitted over an unnormalised mean / non-count cell → `PULSE_OVERLAY_PARAM_MISSING`; explicit `cell_value_pct` on a normalised host → same) are the family-wide vocabulary — nine sample-size modes, three of them distinct-KEY: [`pairwise-n-sources`](pairwise-n-sources.md). `params.letters` (object, unset) — `{}` or `{"alpha": a}`, `0 < a < 1` (else `PULSE_OVERLAY_PARAM_MISSING`, predict and runtime): adds the `letters` part.

- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`) + `Response.Components.Crosstab`. Components-disabled host (options or request) → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime.

## Output

MATRIX (`Payload.Shape = "matrix"`). PAIR axis = one entry per evaluated `(i, j)` pair, key = the 2-tuple of the legs' labels; OPPOSITE axis echoes the host's other axis. Cell = the pair's two-sided p-value, absent when a leg is unreadable or the test degenerate. `row` scope → rows = pairs; `column` transposes.

`payload.parts.letters` (with `params.letters`) — MATRIX on the HOST coordinates. A cell of a compared pair-axis index holds `{letter, group, higher, above}`: its letter (`A`, `B`, … `Z`, `AA`, in axis order, restarting per comparison group — one group, or one per `pair_along_dim` bucket), the group, the letters of same-group indexes its figure beats (the test statistic's sign) and the significant subset. Significant = raw p < `0.05` (or `alpha`, echoed as `summary.parameters.letters_alpha`); under a correction `significant_adjusted` (an explicit `alpha` compares `p_adjusted`). An index compared to nothing — the implicit Total column, a lone bucket member — has no cell. A multi-measure host letters each measure's rows.

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: The normal approximation needs roughly 10 successes and 10 failures per leg, and an n_source or p_source that does not match the cell values skips pairs.
- `parts.letters.cells.value`: Significance letters for this host cell: its column's (or row's) letter, and in above the letters of the same-group columns it is significantly higher than here.

## Gotchas

- An undefined (NaN) host cell is MISSING: its pairs skip as an absent cell's do (`PULSE_STATS_SKIP_EXTRACT_FAILED`).
- Weighted host (both kinds, any source): p̂ = the weighted cell share (Σw_success/Σw_base); with `n_source` omitted n = the N* of that base, the cell's normalise denominator (`sum_weights` frequency, `n_eff` probability); `row_margin_n` / `column_margin_n` read a displayed margin's N*. Unweighted-count modes (and `cell_weight_sum` / `cell_value_weighted` under probability) are `PROCESSING_CONFIG` in predict AND runtime ([`pairwise-n-sources`](pairwise-n-sources.md)). `Summary.Parameters` adds `sum_weights` (+ `n_eff`).
- Reuses `twoProportionZ` — byte-for-byte equal to every two-proportion z test and overlay on the same (success, n).
- The primary stays RAW p-values; direction and significance ride `letters` only when asked; min-n flags are the embedder's job.
- Degenerate pairs (n=0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- **`p_source` mismatch empties the layer.** A leg outside `[0,1]` (`cell_value` over a raw count or a 0..100 percentage) skips EVERY pair (`PULSE_OVERLAY_REF_ZERO`, reason `PULSE_STATS_SKIP_PROPORTION_INVALID`). A count cell needs `normalize: row` (row scope) first.
- **The three distinct-KEY n modes are admitted only on a distinct-key cell aggregator** (sum-of-distinct or distinct count) — the modal aggregators spell a `distinct_count` too, but theirs counts answer codes. `n_within_distinct` additionally refuses a fan-out grouper at a summed-across depth (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`, predict AND runtime); the margin modes are exact by construction and never gated. Null rules and the direct-caller bypass: [`pairwise-n-sources`](pairwise-n-sources.md).
- Flagged buffered in `OverlayStreamability`, but the HOST crosstab still FUSES on a mergeable cell aggregator (a weighted mean included, even over a per-element set axis).
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-probit-t`](op-overlay-pairwise-probit-t.md), [`op-overlay-prop-z-cell`](op-overlay-prop-z-cell.md), [`op-test-prop-z`](op-test-prop-z.md).
