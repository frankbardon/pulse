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

Compose-only. Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Reference` | string | (required) | Anchor for resolution + key-set gates only. |
| `Targets` | []string | (required) | Target slot labels. |
| `params.population` | string | `matrix` | `row` / `column` / `matrix`. |

## Host shape

COMPOSE — MATRIX crosstab. Reference slot anchors schema-match (E7-S7) + key-alignment (E7-S6) but its VALUES are not consumed — rank math reads only target cells. Intentional asymmetry keeps RANK orthogonal to comparison family (INDEX / DELTA / PROP_Z / T / CHISQ).

## Output

MATRIX — `Cells[r][c].Value` = 1-based rank (1 = largest) within the selected population. Mirrors target matrix's RowKeys / ColumnKeys. Layer `Baseline` unset.

## Gotchas

- Tie-breaking: average rank (matches `scipy.stats.rankdata` default).
- Absent target cell stays absent on the overlay — does NOT participate in the population's denominator. Ranks over PRESENT cells only.
- `population=row` → rank within the cell's own row; `column` → within column; `matrix` → across all present cells.
- Buffered (Compose host always buffered by the slot barrier; ranking needs the full materialised matrix anyway).

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-share-of-total`.
