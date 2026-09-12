---
name: op-overlay-rank
kind: operator
category: OVERLAY
operator: OVERLAY_RANK
description: Compose-host per-cell rank of each target cell within a configurable population (row / column / matrix).
type: reference
applies_to: compose
examples_tags: [overlay, compose, top-n]
---

Compose-only. Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `cell`. `Reference` required — anchor for resolution + key-set gates only. `Targets` required slot labels. `params.population` (string, default `matrix`) — `row` / `column` / `matrix`.

## Host shape

COMPOSE — MATRIX crosstab. The reference slot anchors schema-match + key-alignment but its VALUES are not consumed: rank math reads target cells only. That asymmetry keeps RANK orthogonal to the comparison family (INDEX / DELTA / PROP_Z / T / CHISQ).

## Output

MATRIX — `Cells[r][c].Value` = 1-based rank (1 = largest) within the selected population. Mirrors the target's RowKeys / ColumnKeys. `Baseline` unset.

## Gotchas

- Tie-breaking: average rank (matches `scipy.stats.rankdata` default).
- An absent target cell stays absent and is NOT in the population's denominator — ranks cover PRESENT cells only.
- `population=row` → rank within the cell's row; `column` → within its column; `matrix` → across all present cells.
- Buffered (the slot barrier always buffers, and ranking needs the full materialised matrix).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-share-of-total`.
