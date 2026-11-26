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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `population` | enum | `matrix` | `row` (within the cell's row), `column` (within its column) or `matrix` (across every present cell). |

`scope` must be `cell`. `reference` required — anchors the slot match only. `targets` required slot labels.

## Host shape

COMPOSE — MATRIX crosstab. Slots must match on schema and keys, but the reference's VALUES are never read: ranks come from the target cells only.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = 1-based rank (1 = largest) within the population, on the target's keys. `summary.baseline` unset. One layer per target, in `targets` order: one target keeps `name`; two or more are `<name>__<target>`, each the one-target layer. Over `options.max_panel_targets` (16) → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

<!-- generated: contract -->

## Gotchas

- Tie-breaking: average rank (matches `scipy.stats.rankdata` default).
- An absent target cell stays absent and is NOT in the population's denominator — ranks cover PRESENT cells only.
- Buffered (the Compose fold always buffers).
<!-- feature: AGG_MODE -->- A code cell (`AGG_MODE` over `categorical_*` / `packed_bool`) on either slot → `PROCESSING_CONFIG`, reason `categorical_code`; use a count cell.<!-- /feature -->

## See

- Skills: `overlay-system`, `compose-requests`, `op-overlay-share-of-total`.
