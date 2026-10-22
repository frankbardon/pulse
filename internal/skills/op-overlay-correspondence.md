---
name: op-overlay-correspondence
kind: operator
category: OVERLAY
operator: OVERLAY_CORRESPONDENCE
description: Correspondence analysis of the host crosstab — inertia per dimension, row and column map coordinates, quality, contribution and chi-square profile distances as parts.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `dimensions` — default 2, capped at `K = min(r, c) − 1`; explicit `> K` ⇒ `PULSE_OVERLAY_PARAM_MISSING`.
- `normalization` — `symmetric` (default) | `row_principal` | `column_principal`.

## Host shape

MATRIX crosstab of count or sum cells.

## Output

SERIES of `K` inertias, keys `dim_k` (`share`, `cumulative_share`, `singular_value`); `Summary.statistic` = total inertia. Parts: `row_` / `column_coordinates`, `row_` / `column_contribution` (MATRIX); `row_` / `column_quality` (SERIES); `row_` / `column_distance` (MATRIX, kept keys × kept keys) — pairwise chi-square profile distances, metric `chi_square` over the other axis's masses: symmetric, 0 diagonal, full-space (ignore `dimensions` / `normalization`).

<!-- generated: reading-the-output -->

## Reading the map

Plot dim_1 × dim_2: close same-kind points ⇒ similar profiles. Row-to-column closeness reads only in a principal/standard map. Check `cumulative_share` and quality first. Axes may mirror R `ca`. A low retained share ⇒ read `row_distance` / `column_distance`, not the map.

<!-- generated: contract -->

## Gotchas

- Other cell, or a negative / undefined (NaN) cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Zero-total rows/columns dropped (`PULSE_OVERLAY_REF_ZERO`); < 2×2 left ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Scale-invariant: a weighted host needs no Kish scaling.

## See

- Skills: `overlay-system`, `op-overlay-std-residual`, `op-overlay-chisq-matrix`.
