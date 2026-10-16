---
name: compose-banner
description: ComposedRequest.banner — a survey banner table (stub sections × column groups + a Total block) as ONE Compose request, expanded into ordinary crosstab slots and stitched back into ComposedResponse.banner with per-block significance letters.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, BannerSpec, BannerResult, pulse_compose]
requires: [capability:compose_banner, capability:crosstab]
---

# Compose banners

A **banner** is the survey table "Total | Gender | Age | Region" across several KPI stubs, written once. Before any slot runs, Pulse expands it into ordinary crosstab slots — per section, the Total slot, then one slot per column group — appended after `requests` (and any sweep slots). Each slot runs exactly like a hand-written one (`compose-requests`), then `banner` on the response stitches each section's slots side by side.

```jsonc
{
  "banner": {
    "request": {
      "cohort": {"filename": "survey.pulse"},
      "crosstab": {
        "rows": [{"type": "<category grouper>", "field": "brand"}],
        "cell": {"type": "<count>", "field": "brand"},
        "normalize": "column",
        "margins": {"rows": true, "columns": true, "grand": true}
      }
    },
    "sections": [
      {"label": "all"},
      {"label": "aware", "filterers": [{"type": "<value filter>", "field": "aware", "values": ["yes"]}]}
    ],
    "groups": [
      {"label": "Gender", "columns": [{"type": "<category grouper>", "field": "gender"}]},
      {"label": "Age", "columns": [{"type": "<category grouper>", "field": "age"}]}
    ],
    "total": true,
    "overlays": [{"name": "sig", "kind": "<pairwise kind>", "scope": "column", "params": {"letters": {}}}]
  }
}
```

## Shape

- `request` is the base every slot starts from: cohort, weight, filterers, `return` and the shared crosstab (rows, cell, margins, normalize). It may not set `label`, crosstab `columns` (the groups own them) or crosstab `shape: long` (the stitch reads the matrix).
- `sections` (at least one): each a unique `label`, plus optional `filterers` APPENDED to the base's — a section has its own base (all adults vs aided-aware) — and optional `rows` / `cell` / `cells` REPLACING the base crosstab's (several `cells` = several measures as stub rows, `crosstab-measures`).
- `groups`: each a unique `label` and a non-empty `columns` grouper list. Empty only with `total: true`.
- `total: true` adds the Total block: a crosstab with no column grouper (the implicit Total column), stitched first. No group may then be labelled `Total`.
- `overlays` are request overlays appended to every GROUP slot after the base's own — never to the Total slot.

Slot labels are `<section>:<group>` (`all:Total`, `aware:Gender`); they must stay unique across the batch (`PULSE_COMPOSE_LABEL_COLLISION`). Compose overlays may name banner slots by them. Structural faults are `PULSE_BANNER_INVALID` with `details.reason` (`request_missing`, `request_label_set`, `columns_set`, `shape_long`, `sections_empty`, `groups_empty`, `label_empty`, `label_duplicate`, `label_reserved`, `columns_empty`) and `details.field`; everything else is judged on the expanded slot exactly as on its hand-written twin.

## The stitched table

`banner.sections[i]` per section: `row_header` / `row_keys` (the union of the blocks' rows, first seen first), `column_keys` (every block's keys concatenated), `cells[row][column]`, `column_margins`, `cell_label` / `cell_labels`, `normalize_applied`, and `blocks[]` — `{label, slot, total, start, count, column_header, row_margins, grand_total, measures}`. Every figure is COPIED from its slot, never recomputed. A record reaches a block's margin only if it reached a CELL of that block, so row margins and grand totals are per block. A row a block lacks is absent there.

## Letters

A banner overlay carrying `params.letters` adds `letters[]` per section — `{overlay, name, kind, scope, cells}` on the stitched coordinates, each present cell `{letter, group, higher, above}`. Under `column` scope the letters run on across the banner (Gender A, B; Age C, D, E; …): each block keeps its own comparison groups, so a column is compared only with columns of its own group, never across groups. The **Total block is compared to nothing**: it never receives the banner overlays, so its columns carry no letter cell. Under `row` scope cells are copied unchanged.

<!-- feature: capability:multiplicity -->
A batch `multiplicity` block with family `compose` pools every banner slot's tests; letters are recomputed after the batch fold, so `above` reads the corrected significance.
<!-- /feature -->

## Limits, predict, return, stream

- `MaxComposeSlots` counts explicit + sweep + banner slots (sections × (groups + Total)) before any runs: `PULSE_LIMIT_EXCEEDED`, serial and parallel alike.
- `PredictCompose` / `pulse_predict` expands the same way and reports `banner {sections, groups, total, expanded_count, labels}`.
- A banner slot always computes its whole response (the stitch reads it unshaped); its `return` still trims it on the wire. `banner` sits outside the compose `return`: always emitted, never rounded. MCP fills a missing base-request `return` with the `standard` default.
- `--echo-request` echoes the expanded slots; `--stream` ends with one `{"banner":{...}}` line.
- Cost: N scans of the cohort, one per slot — `ComposeParallel` overlaps them.

## See

`compose-requests` (slots, labels, parallel) · `crosstab-guide` (the slot crosstab) · `crosstab-measures` (several measures as stub rows) · `pairwise-n-sources` (the pairwise kinds and letters)<!-- feature: capability:compose_sweep --> · `compose-sweeps` (the other expansion)<!-- /feature -->.
