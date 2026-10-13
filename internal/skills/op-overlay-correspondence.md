---
name: op-overlay-correspondence
kind: operator
category: OVERLAY
operator: OVERLAY_CORRESPONDENCE
description: Correspondence analysis of the host crosstab — inertia per dimension, row and column map coordinates, quality and contribution as parts.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` `matrix`. `Ref` empty. `Level`/`Within` `0`.

- `dimensions` — retained dims; default 2 capped at `K = min(r, c) − 1`; explicit `> K` ⇒ `PULSE_OVERLAY_PARAM_MISSING`.
- `normalization` — `symmetric` (default; both principal) | `row_principal` | `column_principal` (that axis principal, the other standard).

## Host shape

MATRIX crosstab of count or sum cells.

## Output

SERIES of the `K` principal inertias, keys `dim_k` (`share`, `cumulative_share`, `singular_value`). `Summary.statistic` = total inertia (χ²/n). Parts: `row_coordinates` / `column_coordinates`, `row_contribution` / `column_contribution` (MATRIX points × `dim_1…dim_d`); `row_quality` / `column_quality` (SERIES, cos² over retained dims).

<!-- generated: reading-the-output -->

## Reading the map

Plot dim_1 × dim_2. Same-kind points close ⇒ similar profiles; far from origin ⇒ distinctive. Row-to-column closeness reads only in a principal/standard map. Check `cumulative_share` and quality first.

**Sign:** linalg.SVD — per dim, the column singular vector's dominant entry is positive. Axes may mirror R `ca`.

## Gotchas

- Other cell or negative cell ⇒ `PULSE_OVERLAY_NONNEGATIVE_REQUIRED`.
- Zero-total rows/columns dropped: one `PULSE_OVERLAY_REF_ZERO` per axis. < 2×2 left ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Scale-invariant: a weighted host's Σw table needs no Kish scaling.
- Zero-inertia dim ⇒ `null` coordinates.

## See

- Skills: `overlay-system`, `op-overlay-std-residual`, `op-overlay-chisq-matrix`.
