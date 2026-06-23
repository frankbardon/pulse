---
name: op-overlay-pairwise-prop-z
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROP_Z
description: Intra-matrix axis-pairwise pooled-SE two-proportion z-test (row-vs-row or col-vs-col within one crosstab).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Overlays decorate the host result; they do not emit `Response.Components` (but this family READS them).

Intra-matrix pairwise: tests one host-matrix slot against another ALONG one axis of the SAME crosstab — the per-Request counterpart to the Compose-host `OVERLAY_PROP_Z_PANEL` (which pairs across slots). ROW scope pairs row indices for each column; COLUMN scope pairs column indices for each row.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `row` (pair rows per column) or `column` (pair columns per row). |
| `Ref` | object | (empty) | Intra-matrix — leave empty. Any populated arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. |
| `params.pair_along_dim` | int | (unset) | Restrict pairs to "all pair-axis dims agree except this one" buckets. Unset = every pair. |
| `params.n_source` | enum | `cell_n_unweighted` | Sample-size leg: `cell_n_unweighted` / `cell_value_weighted` / `row_margin_n` / `column_margin_n` / `n_within` / `cell_weight_sum`. |
| `params.n_within_depth` | int | `0` | With `n_source=n_within`, fixes the first depth+1 pair-axis dims in the denominator (mirrors `CrosstabSpec.NormalizeWithin`). |
| `params.p_source` | enum | `cell_value_pct` | `cell_value_pct` (cell is a 0..100 percentage, ÷100) or `cell_value` (already 0..1). |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`) + `Response.Components.Crosstab`. A components-disabled host fires `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — `OverlayLayer.Payload.Shape = "matrix"`. The PAIR axis carries one entry per evaluated `(i, j)` index pair (key = the 2-tuple of the compared legs' labels); the OPPOSITE axis echoes the host's other axis. Cell `(pair, opp)` = the pair's two-sided p-value, absent when a leg is unreadable or the test degenerate. ROW scope → rows = pairs, cols = host columns; COLUMN scope transposes.

## Gotchas

- Reuses `twoProportionZ` — p-values match `OVERLAY_PROP_Z_CELL` / `TEST_PROP_Z` byte-for-byte for the same (success, n) inputs.
- Emits RAW p-values only. Direction (which leg is greater), thresholds, and min-n flags are the embedder's job — every input for them is already on the response (host cells, per-cell n).
- Degenerate pairs (n=0, pooled ∈ {0,1}, zero SE) fold into one aggregated `PULSE_OVERLAY_REF_ZERO` warning per reason.
- Buffered (inferential).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-probit-t`, `op-overlay-prop-z-cell`, `op-test-prop-z`.
