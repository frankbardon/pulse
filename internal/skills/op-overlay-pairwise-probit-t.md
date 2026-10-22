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

Probit-transformed t between host slots ALONG one axis of the SAME crosstab — sibling of the pairwise proportion z: same share + n inputs, probit transform instead. Shared pairwise contract (spec, grid, letters, skips, weighted legs): `overlay-pairwise`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `pair_along_dim` | int | — | Restrict pairs to same-bucket comparisons on the pair axis. |
| `n_within_depth` | int | `0` | With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise. |
| `letters` | object | — | `{}` or `{"alpha": a}`, `0 < a < 1`: adds the `letters` part. |
| `n_source` | enum | — | Where each leg's n is read; omitted = the cell's base (the normalise denominator on a normalised host; weighted, its N*). |
| `p_source` | enum | — | How each leg's share is read; omitted = `cell_value` on a normalised host, else `cell_value_pct`. |

`Scope` (required) — `row` or `column`. `Ref` empty. n and share vocabulary, identical to the pairwise proportion z: `pairwise-n-sources`.

## Host shape

MATRIX crosstab + `Response.Components.Crosstab` (components off → `PULSE_OVERLAY_COMPONENTS_REQUIRED`, predict and runtime). A code cell is refused (`overlay-pairwise`).

## Output

MATRIX — pair × opposite-axis grid of two-sided p-values (layout: `overlay-pairwise`). Per pair: `t = (Φ⁻¹(p_i) - Φ⁻¹(p_j)) / sqrt(1/n_i + 1/n_j)` against Student-t with `df = n_i + n_j - 2`; proportions clipped to `[1e-10, 1-1e-10]` before Φ⁻¹. A design formula (market-research convention), pinned as is to R at 1e-10: the `sqrt(1/n_i + 1/n_j)` SE is not a probit share's real spread and no stock test computes it, so read the p as a convention, not a calibrated test. `letters`: `higher` from the sign of `t`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `df <= 0` (n_i + n_j <= 2) or a non-finite t skips the pair (aggregated `PULSE_OVERLAY_REF_ZERO`).
- `p_source` mismatch (a leg outside `[0,1]`) empties the layer — never clipped; normalize a count cell first.
- Distinct-KEY n modes: the same admission and slab partition refusal as the proportion z (`pairwise-n-sources`).
- The host crosstab still FUSES on a mergeable cell aggregator.

## See

- Skills: `overlay-pairwise`, `pairwise-n-sources`, `overlay-system`, `crosstab-guide`, `op-overlay-pairwise-prop-z`, `op-test-t`.
