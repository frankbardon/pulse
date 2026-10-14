```yaml
name: op-overlay-correspondence
kind: operator
category: OVERLAY
operator: OVERLAY_CORRESPONDENCE
description: Correspondence analysis of the host crosstab — inertia per dimension, row and column map coordinates, quality, contribution and chi-square profile distances as parts.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Correspondence analysis of a crosstab: a perceptual map placing rows and columns in a few dimensions so similar profiles sit close together.

Questions it answers:

- Which brands sit closest to which attributes on a perceptual map?
- Which segments share a similar mix of answers, and which answers set each apart?

Use something else:

- `OVERLAY_CHISQ_MATRIX` when you want one test of whether rows and columns are associated.

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `dimensions` — default 2, capped at `K = min(r, c) − 1`; explicit `> K` ⇒ `PULSE_OVERLAY_PARAM_MISSING`.
- `normalization` — `symmetric` (default) | `row_principal` | `column_principal`.

## Host shape

MATRIX crosstab of count or sum cells.

## Output

SERIES of `K` inertias, keys `dim_k` (`share`, `cumulative_share`, `singular_value`); `Summary.statistic` = total inertia. Parts: `row_` / `column_coordinates`, `row_` / `column_contribution` (MATRIX); `row_` / `column_quality` (SERIES); `row_` / `column_distance` (MATRIX, kept keys × kept keys) — pairwise chi-square profile distances, metric `chi_square` over the other axis's masses: symmetric, 0 diagonal, full-space (ignore `dimensions` / `normalization`).

## Reading the output

- `summary.statistic`: In each series entry (one per dimension, dim_1 first): the dimension's principal inertia, the part of the table's association it accounts for. On the layer summary: the total inertia, the chi-square statistic divided by the grand total; 0 means rows and columns are independent.
- `summary.parameters.share`: In each series entry: the dimension's share of the total inertia (0 to 1); cumulative_share adds the dimensions before it.

## Reading the map

Plot dim_1 × dim_2: close same-kind points ⇒ similar profiles. Row-to-column closeness reads only in a principal/standard map. Check `cumulative_share` and quality first. Axes may mirror R `ca`. A low retained share ⇒ read `row_distance` / `column_distance`, not the map.

## Gotchas

- Other cell, or a negative / undefined (NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Zero-total rows/columns dropped (`PULSE_OVERLAY_REF_ZERO`); < 2×2 left ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Scale-invariant: a weighted host needs no Kish scaling.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-std-residual`](op-overlay-std-residual.md), [`op-overlay-chisq-matrix`](op-overlay-chisq-matrix.md).
