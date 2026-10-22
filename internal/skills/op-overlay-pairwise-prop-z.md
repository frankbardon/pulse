---
name: op-overlay-pairwise-prop-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROP_Z
description: Intra-matrix axis-pairwise pooled-SE two-proportion z-test (row-vs-row or col-vs-col within one crosstab).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Pooled-SE two-proportion z between host slots ALONG one axis of the SAME crosstab — the per-Request counterpart to Compose's proportion-z panel. Shared pairwise contract (spec, grid, letters, skips, weighted legs): `overlay-pairwise`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to buckets agreeing on all pair-axis dims but this one; unset = every pair. |
| `n_within_depth` | int | `0` | With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |
| `n_source` | enum | — | Where each leg's n is read; omitted = the cell's base (the normalise denominator on a normalised host; weighted, its N*). |
| `p_source` | enum | — | How each leg's share is read; omitted = `cell_value` on a normalised host, else `cell_value_pct`. |

`Scope` (required) — `row` or `column`. `Ref` empty. The nine n modes (three distinct-KEY) and the `p_source` refusals: `pairwise-n-sources`.

## Host shape

MATRIX crosstab + `Response.Components.Crosstab` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime). The cell must be a count or a share: a code cell is refused (`overlay-pairwise`).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values; `payload.parts.letters` with `params.letters` (`higher` from the sign of z). Layout: `overlay-pairwise`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Reuses `twoProportionZ` — byte-for-byte equal to every two-proportion z test and overlay on the same (success, n).
- Degenerate pairs (n = 0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` per reason.
- `p_source` mismatch (a leg outside `[0,1]`) empties the layer; normalize a count cell first.
- Distinct-KEY n modes need a distinct-key cell aggregator and a partitioned slab (`PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED`): `pairwise-n-sources`.
- Weighted host: p̂ is the weighted share, n the base's N*; unweighted-count modes are `PROCESSING_CONFIG` (`overlay-pairwise`).
- The host crosstab still FUSES on a mergeable cell aggregator.

## See

- Skills: `overlay-pairwise`, `pairwise-n-sources`, `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-probit-t`, `op-overlay-prop-z-cell`, `op-test-prop-z`.
