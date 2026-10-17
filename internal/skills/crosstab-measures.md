---
name: crosstab-measures
description: Several measures as stub rows in one crosstab — crosstab.cells, the MEASURE row position, per-measure margins and components, normalize and pairwise overlays per measure
type: guide
kind: design
applies_to: process, compose, predict
covers: [Crosstab, CrosstabComponents, cells]
requires: [capability:crosstab, capability:crosstab_cells]
---

# Crosstab measures

`crosstab.cells` lists several cell aggregations; each becomes a stub row under every row key. Reach for it when one banner table shows several figures per key — a count, a mean and a top-box share stacked under each brand — read against the same columns in one scan.

```jsonc
{"crosstab": {
  "rows":    [{"type": "<grouper>", "field": "brand"}],
  "columns": [{"type": "<grouper>", "field": "wave"}],
  "cells": [
    {"type": "<count>", "field": "id",    "label": "base"},
    {"type": "<mean>",  "field": "score", "label": "mean_score"}
  ],
  "margins": {"columns": true, "grand": true}
}}
```

## Rules

- `cell` and `cells` are mutually exclusive. A null entry, or an entry smart defaults cannot type (no `type` and a field it infers nothing for), is `PULSE_CROSSTAB_CELLS_INVALID`. An entry with a `field` and no `type` is defaulted from the field's schema type, one entry at a time.
- Effective labels (`label`, else `TYPE_field`) must be unique — `PULSE_CROSSTAB_CELLS_DUPLICATE_LABEL` — and distinct from every `margin_aggregations` label.
- **Each measure runs exactly as a single-cell crosstab with that `cell` would.** Its own cells, margins, normalize denominators, auxiliary margins and components; nothing is pooled across measures. A `normalize: "column"` table reads each measure's column share against that measure's column margin.

## Matrix layout

The row axis gains an innermost MEASURE position: `row_header.fields` ends with `_measure` and `row_header.types` with `MEASURE`, and spec row r becomes one row per measure, keys `[...row key, measure label]`, measures in declaration order (row r, measure m at index r × M + m). `matrix.cell_labels[i]` names row i's measure; `cell_label` is empty.

Row margins fold with the rows. Column margins and the grand total are per measure, so they move to `matrix.measures[m]` — `{label, column_margins, grand_total}` — and the single-measure `column_margins` / `grand_total` stay empty.

`shape: "long"` emits each measure's rows in declaration order, every row (margin rows included) stamped `_measure`.

## Components

`components.crosstab` folds like the matrix: `cell_counts`, `cell_components`, `row_key_components` and the row-margin figures gain the measure rows; the column-margin and grand-total figures move to `components.crosstab.measures[m]`. Auxiliary `margin_aggregations` figures are evaluated per measure into the same slots. **A record reaches an auxiliary margin only if it reached a CELL** — both axis keys resolved — exactly as on a single-cell crosstab.

## Overlays

Only the `OVERLAY_PAIRWISE_*` kinds run on a multi-measure crosstab: once per measure, and pairs never cross measures. A column-scope pair reads row r × M + m; a row-scope pair compares the folded rows of one measure. Every other request overlay, and any Compose overlay reading a multi-measure slot, is refused with `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.reason: "multi_measure_host"`) — predict and runtime alike.

## Predict and execution

Predict validates every entry, defaults each untyped one (`defaults_applied` path `["Crosstab", "Cells", "<i>", "Type"]`), and counts one p-value per pair per measure. The fused path (`crosstab-guide`) runs one accumulator per measure per cell in lockstep; it fuses only when every measure would fuse alone. Fused and buffered output are identical.

## See

`crosstab-guide`, `crosstab-margin-aggregations`, `response-components`, `overlay-system`, `pairwise-n-sources`.
