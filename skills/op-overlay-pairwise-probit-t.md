---
name: op-overlay-pairwise-probit-t
kind: operator
category: OVERLAY
operator: OVERLAY_PAIRWISE_PROBIT_T
description: Intra-matrix axis-pairwise probit t-test (Φ⁻¹ transform of each leg's proportion, Student-t tail).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test, pairwise]
---

Intra-matrix pairwise: one host-matrix slot against another ALONG one axis of the SAME crosstab. `row` scope pairs row indices per column; `column` pairs column indices per row. Sibling of `OVERLAY_PAIRWISE_PROP_Z` — same proportion + n inputs, probit transform instead. Overlays decorate the host result; they do not emit `Response.Components` (this family READS them).

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `row` or `column`. |
| `Ref` | object | (empty) | Intra-matrix — leave empty. |
| `params.pair_along_dim` | int | (unset) | Restrict pairs to same-bucket comparisons on the pair axis. |
| `params.n_source` | enum | `cell_n_unweighted` | Sample-size leg (see `op-overlay-pairwise-prop-z`). |
| `params.n_within_depth` | int | `0` | Within-group denominator depth for `n_source=n_within`. |
| `params.p_source` | enum | `cell_value_pct` | `cell_value_pct` or `cell_value`. |

## Host shape

MATRIX crosstab + `Response.Components.Crosstab`. Components-disabled host → `PULSE_OVERLAY_COMPONENTS_REQUIRED`.

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values, layout identical to `op-overlay-pairwise-prop-z`. Per pair: `t = (Φ⁻¹(p_i) - Φ⁻¹(p_j)) / sqrt(1/n_i + 1/n_j)` against Student-t with `df = n_i + n_j - 2`, two-sided; proportions clipped to `[1e-10, 1-1e-10]` before Φ⁻¹. Reuses the Student-t survival helper backing `TEST_T`.

## Gotchas

- `df <= 0` (n_i + n_j <= 2) or a non-finite t skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- RAW p-values only — direction / thresholds / min-n are the embedder's job.
- `p_source` mismatch fails silently: `cell_value` over a 0..100 percentage pushes proportions outside `[0,1]` and every pair skips.
- Flagged buffered in `OverlayStreamability`; the HOST crosstab still FUSES on a mergeable cell aggregator.

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-prop-z`, `op-test-t`.
