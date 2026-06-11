---
name: overlay-system
description: Result overlay decoration framework — kinds, shapes, scopes, refs, streaming model. Use when adding a derived projection (index, sibling delta, baseline lift, population comparison) to a primary result without re-deriving on the client side.
type: guide
applies_to: process, compose, facet
---

# Overlay system

<skill_overview>
An overlay is an additive, request-driven derived projection that decorates a primary result (today: crosstab matrices; later: regressions, time series, group results) with one or more secondary number grids — index-vs-margin scores, sibling comparisons, baseline lifts, population deltas, multi-stage chain references.

Every overlay shares one declarative surface (`OverlaySpec` on `Request.Overlays`) and one structured response surface (`OverlayLayer` on `Response.Overlays`). Renderers consume the base result and the overlay layers side-by-side without re-deriving the projection. Specs and layers appear in matching index order — `Response.Overlays[i]` always corresponds to `Request.Overlays[i]`.

Use this skill when you need to add a derived score on top of a result you already know how to compute. The overlay system is NOT a place to put new base aggregations — those are `AGG_*` operators. Overlays decorate; they do not replace.
</skill_overview>

## Catalog

Every overlay kind ships as a row in this table. New kinds extend `types.AllOverlayKinds()`, the `OverlayStreamability` map, and `descriptor.overlayCapabilityFor`. The catalog gate (`TestSkillsCoverAllOverlayKinds` in `skills/coverage_test.go`) iterates `types.AllOverlayKinds()` and asserts every kind is mentioned in this file.

| Kind | Scope | Shape | Streamable | RefFamily | Description |
|---|---|---|---|---|---|
| `OVERLAY_INDEX_VS_MARGIN` | `cell` | `matrix` | no (buffered) | `Margin` | Per-cell index score `100 * cell / margin` against the matching axis margin. E1 supports CELL scope over a MATRIX (crosstab) host; ROW / COLUMN / TOTAL ship in later epics alongside the matching payload shapes. |

## The three-shape model

Every overlay payload is one of three shapes. The shape is independent of the scope — a CELL-scoped overlay typically produces a matrix payload, a ROW-scoped overlay typically produces a series, but the choice is per-overlay.

| Shape | Carries | Typical use | Go field |
|---|---|---|---|
| `scalar` | one `float64` | Total-scoped index, single sibling-vs-baseline delta | `OverlayPayload.Scalar` (`*float64`) |
| `series` | one `float64` per axis key, paired with `Keys` | Row-wise index strip, per-column deviation strip | `OverlayPayload.Series` (`*SeriesPayload`) |
| `matrix` | full row × column grid of `float64` cells | CELL-scoped overlay layered on top of a crosstab matrix base | `OverlayPayload.Matrix` (`*MatrixPayload`, reused from crosstab) |

`OverlayPayload.Shape` echoes which field is populated. Exactly one is meaningful per layer. The matrix shape reuses `crosstab.MatrixPayload` directly so renderers handle the overlay grid with the same row/column header machinery as the base.

`SeriesPayload` is minimal — `Keys []string` and `Values []float64` of equal length, in matching index order. Later series-bearing families (time-series overlays, sparkline projections) may extend it additively without breaking the existing JSON contract.

## Scopes

The scope declares where the overlay lands relative to the base result. Renderers branch on scope when deciding how to lay the overlay grid on top of the base.

| Scope | Decorates | Typical shape |
|---|---|---|
| `cell` | every (row_key, column_key) cell of the base | `matrix` |
| `row` | every row tuple of the base | `series` (keyed by row tuple) |
| `column` | every column tuple of the base | `series` (keyed by column tuple) |
| `matrix` | the matrix as a whole (e.g. a column-normalized re-projection) | `matrix` |
| `group` | one grouper level — reserved for future nested-axis families | n/a (rejected today) |
| `total` | the grand-total margin slot — one scalar covering the whole result | `scalar` |

E1 supports `cell` only for `OVERLAY_INDEX_VS_MARGIN`. Asking for any other scope returns `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (with the per-kind supported scope list in the message).

## Reference families

Every overlay declares what it compares against via `OverlayRef`. The ref is a discriminated union — exactly one pointer field is meaningfully populated per spec, and the validator rejects a spec that populates the wrong pointer for its kind.

| Family | Go field | What it points to | Status |
|---|---|---|---|
| Margin | `Ref.Margin *OverlayMarginRef` | An axis margin slot (`row`, `column`, `grand`) of the base result | E1: live for `OVERLAY_INDEX_VS_MARGIN` |
| Sibling | `Ref.Sibling *OverlaySiblingRef` | Another cell on the same axis (specific axis-key value) | Reserved |
| BaselineIndex | `Ref.BaselineIndex *OverlayBaselineIndexRef` | A fixed baseline coordinate (row tuple + column tuple) | Reserved |
| Population | `Ref.Population *OverlayPopulationRef` | An alternate cohort / population (filtered-vs-unfiltered comparison) | Reserved |
| Stage | `Ref.Stage *OverlayStageRef` | An earlier `ChainRequest` stage's output | Reserved |
| Slot | `Ref.Slot *OverlaySlotRef` | A named slot of the base result (e.g. labelled regression coefficient) | Reserved |

The reserved pointers are placeholder slots so later stories drop in without re-opening `types/overlay.go` and without breaking embedder-side JSON. Today only `Ref.Margin` is consumed.

### Margin reference detail

`OverlayMarginRef.Axis` is the discriminator. Three values are valid:

| Axis | Denominator |
|---|---|
| `row` | the row-margin vector (Σ over columns per row key) |
| `column` | the column-margin vector (Σ over rows per column key) |
| `grand` | the grand-total margin (Σ over every filter-passing row) |

Unknown values fire `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Spec-order viz contract

`Response.Overlays` is order-preserving. Index `i` in the response always matches index `i` in `Request.Overlays`. Renderers MAY rely on this — there is no implicit reordering, no dedup, no inferred grouping. If you submit three overlay specs you get three overlay layers, in your submitted order, regardless of what the kinds compute.

Two specs that compute identical numbers still produce two `OverlayLayer` entries. The framework does not collapse duplicates because the renderer-facing `Name` may differ (one labelled "vs row", one labelled "vs grand") even if the math accidentally agrees.

Empty `Request.Overlays` is omitempty — the JSON field is omitted entirely.

## Streamable vs buffered

`types.OverlayStreamable(kind)` is the single source of truth for whether a kind can ride the streaming Process path or forces the orchestrator down a buffered route. `OverlayStreamability` (a map keyed by `OverlayKind`) declares the answer; `descriptor.OverlayCapabilities()` reflects it as `Buffered = !streamable` on the manifest.

Today every registered overlay is buffered. `OVERLAY_INDEX_VS_MARGIN`'s host crosstab is always buffered (margins are recomputed from raw rows — see `skills/crosstab-guide.md` and CLAUDE.md "Execution modes" → Crosstab), so the overlay inherits the buffered footprint at no extra cost.

A future row-share overlay that can be folded into the streaming fused crosstab pass would flip its row in `OverlayStreamability` to `true` and add the matching gate plumbing in the processing layer. The static table is the gate — `TestStreamability_OverlaysKnown` enforces completeness, and unknown kinds fall through to "buffered" by default so a missed table edit cannot accidentally let an unknown kind stream.

## Worked example: `OVERLAY_INDEX_VS_MARGIN` against a 2-axis crosstab

A 2-axis crosstab counts sales by region × segment. An index overlay decorates each cell with `100 * cell / row_margin` so the renderer can colour-ramp cells that over- or under-perform the row average.

### Request

```json
{
  "cohort": {"filename": "sales.pulse"},
  "crosstab": {
    "rows":    [{"type": "GROUP_CATEGORY", "field": "region"}],
    "columns": [{"type": "GROUP_CATEGORY", "field": "segment"}],
    "cell":    {"type": "AGG_COUNT", "field": "id", "label": "n"}
  },
  "overlays": [
    {
      "name": "i_row",
      "kind": "OVERLAY_INDEX_VS_MARGIN",
      "scope": "cell",
      "ref": {"margin": {"axis": "row"}}
    }
  ]
}
```

### Response

```json
{
  "data": {
    "crosstab": {
      "matrix": {
        "row_keys":    [["North"], ["South"]],
        "column_keys": [["Enterprise"], ["SMB"]],
        "cells": [
          [{"value": 40}, {"value": 60}],
          [{"value": 30}, {"value": 70}]
        ],
        "row_margins":    [100, 100],
        "column_margins": [70, 130],
        "grand_margin":   200
      }
    },
    "overlays": [
      {
        "name":  "i_row",
        "kind":  "OVERLAY_INDEX_VS_MARGIN",
        "scope": "cell",
        "ref":   {"margin": {"axis": "row"}},
        "payload": {
          "shape": "matrix",
          "matrix": {
            "row_keys":    [["North"], ["South"]],
            "column_keys": [["Enterprise"], ["SMB"]],
            "cells": [
              [{"value": 40}, {"value": 60}],
              [{"value": 30}, {"value": 70}]
            ]
          }
        },
        "summary": {
          "min": 30,
          "max": 70,
          "count": 4,
          "baseline": 100
        }
      }
    ]
  }
}
```

Cell math (per row, against the row margin):

| Cell | Raw count | Row margin | Index `100 * cell / margin` |
|---|---|---|---|
| (North, Enterprise) | 40 | 100 | 40 |
| (North, SMB)        | 60 | 100 | 60 |
| (South, Enterprise) | 30 | 100 | 30 |
| (South, SMB)        | 70 | 100 | 70 |

The overlay's `payload.matrix` mirrors the base matrix's row and column key headers — renderers can lay the overlay grid directly on top of the base grid without coordinate translation. `summary.baseline` is `100` so a diverging colour ramp centred on the baseline reads "underperforms / overperforms" at a glance.

### Switching the denominator

Swap `axis` to `column` to compare each cell against its column margin:

| Cell | Raw count | Column margin | Index |
|---|---|---|---|
| (North, Enterprise) | 40 | 70  | ~57.1 |
| (North, SMB)        | 60 | 130 | ~46.2 |
| (South, Enterprise) | 30 | 70  | ~42.9 |
| (South, SMB)        | 70 | 130 | ~53.8 |

Swap `axis` to `grand` to compare each cell against the grand total (`200`):

| Cell | Raw count | Grand margin | Index |
|---|---|---|---|
| (North, Enterprise) | 40 | 200 | 20 |
| (North, SMB)        | 60 | 200 | 30 |
| (South, Enterprise) | 30 | 200 | 15 |
| (South, SMB)        | 70 | 200 | 35 |

### Combining multiple overlays

Multiple specs ride the same `Request.Overlays` slice. Each produces one layer in matching index order:

```json
{
  "overlays": [
    {"name": "i_row",    "kind": "OVERLAY_INDEX_VS_MARGIN", "scope": "cell", "ref": {"margin": {"axis": "row"}}},
    {"name": "i_column", "kind": "OVERLAY_INDEX_VS_MARGIN", "scope": "cell", "ref": {"margin": {"axis": "column"}}},
    {"name": "i_grand",  "kind": "OVERLAY_INDEX_VS_MARGIN", "scope": "cell", "ref": {"margin": {"axis": "grand"}}}
  ]
}
```

`Response.Overlays` carries three layers, indices 0 / 1 / 2, matching the spec order. Renderers can offer the user a dropdown to switch between denominators without re-issuing the request.

## Validation rules (E1)

The descriptor validator (`descriptor.ValidateOverlays`) runs alongside the crosstab / aggregator / test gates. Errors are emitted on the envelope so a caller surfacing multiple structural problems sees them all in one pass.

| Condition | Error code |
|---|---|
| Unknown `kind` | `PULSE_OVERLAY_KIND_UNKNOWN` |
| `OVERLAY_INDEX_VS_MARGIN` without `Ref.Margin` populated | `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` |
| `OVERLAY_INDEX_VS_MARGIN` with unknown `Ref.Margin.Axis` | `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` |
| `OVERLAY_INDEX_VS_MARGIN` without a `Request.Crosstab` host | `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` |
| `OVERLAY_INDEX_VS_MARGIN` with `scope` other than `cell` (E1) | `PULSE_OVERLAY_SCOPE_UNSUPPORTED` |
| Runtime margin denominator is exactly zero | `PULSE_OVERLAY_REF_ZERO` (warning) |

`PULSE_OVERLAY_REF_ZERO` is a warning-class code rather than a hard error. A cell that divides by a zero margin still appears in the payload (the runtime layer surfaces the value as the divide-by-zero policy of the kind — `NaN` for `INDEX_VS_MARGIN`) and the warning lets the renderer flag the affected slots without stopping the rest of the matrix from rendering. Reach the per-code prose via `pulse_errors_lookup` (MCP) or `pulse errors lookup CODE` (CLI).

## Manifest visibility

`pulse manifest --json` (and `pulse_manifest`) carries an `Overlays` capability block — one `OverlayCapability` per `types.AllOverlayKinds()` entry. Each entry declares the kind, the supported shapes, the supported scopes, the consumed ref families, the buffered flag (derived from `OverlayStreamability`), and a short description.

```json
{
  "overlays": [
    {
      "kind": "OVERLAY_INDEX_VS_MARGIN",
      "shapes":    ["matrix"],
      "scopes":    ["cell"],
      "ref_kinds": ["Margin"],
      "buffered":  true,
      "description": "Per-cell index score against the matching axis margin: 100 * cell / margin. E1 supports CELL scope over a MATRIX (crosstab) host with a Margin reference."
    }
  ]
}
```

LLM clients use this block to discover the overlay catalog without inspecting the source. Sorted alphabetically by `kind` for golden stability.

## Adding a new overlay kind

1. Declare the constant in `types/overlay.go` and append it to `AllOverlayKinds()`.
2. Add a row to `OverlayStreamability` in `types/overlay_streamability.go` (the `TestStreamability_OverlaysKnown` gate fails otherwise).
3. Extend the per-kind switch in `descriptor/overlay.go` (`validateOverlaySpec`) and the per-kind capability shape in `descriptor/capabilities_overlay.go` (`overlayCapabilityFor`).
4. Add the processing-layer math under `processing/` and the dispatch hook the host operator (today: crosstab) calls.
5. Add a row to the catalog table at the top of this skill — `TestSkillsCoverAllOverlayKinds` iterates `types.AllOverlayKinds()` and demands every kind appears in this file.

The framework is intentionally additive — new kinds drop in without re-opening the `OverlayRef` union or the `OverlayPayload` discriminated union (both already carry every reserved family pointer).
