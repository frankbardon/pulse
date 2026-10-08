```yaml
name: op-overlay-rank
kind: operator
category: OVERLAY
operator: OVERLAY_RANK
description: Compose-host per-cell rank of each target cell within a configurable population (row / column / matrix).
type: reference
applies_to: compose
examples_tags: [overlay, compose, top-n]
```

Compose-only. Overlays decorate the host; no `Response.Components`.

## Use when

Ranks each cell of a target Compose request's crosstab (1 = largest) within its row, its column or the whole table.

Questions it answers:

- Which product is the top seller in each region?
- Where does each answer rank within its segment?

Use something else:

- `OVERLAY_SHARE_OF_TOTAL` when you want each cell's share of the whole.
- `OVERLAY_ZSCORE_VS_MARGIN` when you want how far each cell sits from its row or column.

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

- Skills: [`overlay-system`](overlay-system.md), [`compose-requests`](compose-requests.md), [`op-overlay-share-of-total`](op-overlay-share-of-total.md).
