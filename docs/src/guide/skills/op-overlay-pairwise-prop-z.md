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

Pooled-SE two-proportion z between host slots ALONG one axis of the SAME crosstab — the per-Request counterpart to Compose's proportion-z panel. Shared pairwise contract (spec, grid, letters, skips, weighted legs): [`overlay-pairwise`](overlay-pairwise.md).

## Use when

Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share?

Questions it answers:

- Which age bands differ from each other in the share choosing each brand?
- Which pairs of regions differ in their share of late deliveries?

Use something else:

- `OVERLAY_PAIRWISE_WELCH_T` when the cells are averages of a numeric measure rather than shares.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to buckets agreeing on all pair-axis dims but this one; unset = every pair. |
| `n_within_depth` | int | `0` | With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |
| `n_source` | enum | — | Where each leg's n is read; omitted = the cell's base (the normalise denominator on a normalised host; weighted, its N*). |
| `p_source` | enum | — | How each leg's share is read; omitted = `cell_value` on a normalised host, else `cell_value_pct`. |

`Scope` (required) — `row` or `column`. `Ref` empty. The nine n modes (three distinct-KEY) and the `p_source` refusals: [`pairwise-n-sources`](pairwise-n-sources.md).

## Host shape

MATRIX crosstab + `Response.Components.Crosstab` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime). The cell must be a count or a share: a code cell is refused ([`overlay-pairwise`](overlay-pairwise.md)).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values; `payload.parts.letters` with `params.letters` (`higher` from the sign of z). Layout: [`overlay-pairwise`](overlay-pairwise.md).

## Reading the output

- `cells.value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: The normal approximation needs roughly 10 successes and 10 failures per leg, and an n_source or p_source that does not match the cell values skips pairs.
- `parts.letters.cells.value`: Significance letters for this host cell: its column's (or row's) letter, and in above the letters of the same-group columns it is significantly higher than here.

## Contract

- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default), `row`, `column`, `request`, `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Reuses `twoProportionZ` — byte-for-byte equal to every two-proportion z test and overlay on the same (success, n).
- Degenerate pairs (n = 0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- `p_source` mismatch (a leg outside `[0,1]`) empties the layer; normalize a count cell first.
- Distinct-KEY n modes need a distinct-key cell aggregator and a partitioned slab (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`): [`pairwise-n-sources`](pairwise-n-sources.md).
- Weighted host: p̂ is the weighted share, n the base's N*; unweighted-count modes are `PROCESSING_CONFIG` ([`overlay-pairwise`](overlay-pairwise.md)).
- The host crosstab still FUSES on a mergeable cell aggregator.

## See

- Skills: [`overlay-pairwise`](overlay-pairwise.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-pairwise-probit-t`](op-overlay-pairwise-probit-t.md), [`op-overlay-prop-z-cell`](op-overlay-prop-z-cell.md), [`op-test-prop-z`](op-test-prop-z.md).
