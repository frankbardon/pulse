# Compose Banners

**Audience:** Go embedders, CLI users and agents who build survey banner
tables: several KPI stubs across a Total column and column groups such as
Gender, Age and Region.

`ComposedRequest.Banner` (`types.BannerSpec`) writes the whole table as
one request. Before any slot runs, Pulse expands it into ordinary
crosstab slots — per section, the Total slot, then one slot per column
group — appended after the explicit `Requests` and any sweep slots. Each
slot runs exactly like a hand-written one: same limits, labels, overlay
and multiplicity folds, serial or parallel. Then
`ComposedResponse.Banner` stitches each section's slots side by side.
The same payload works through `Compose`, `ComposeParallel`,
`PredictCompose`, `pulse api compose`, `pulse api predict-compose`,
`pulse_compose` and `pulse_predict` (`composed`). It is gated by
`capability:compose_banner`.

```json
{
  "multiplicity": {"method": "holm", "family": "compose"},
  "banner": {
    "request": {
      "cohort": {"filename": "survey.pulse"},
      "crosstab": {
        "rows": [{"type": "GROUP_CATEGORY", "field": "brand"}],
        "cell": {"type": "AGG_COUNT", "field": "brand"},
        "normalize": "column",
        "margins": {"rows": true, "columns": true, "grand": true}
      }
    },
    "sections": [
      {"label": "all"},
      {"label": "aware",
       "filterers": [{"type": "FILTER_INCLUDE", "field": "aware", "values": ["yes"]}]}
    ],
    "groups": [
      {"label": "Gender", "columns": [{"type": "GROUP_CATEGORY", "field": "gender"}]},
      {"label": "Age",    "columns": [{"type": "GROUP_CATEGORY", "field": "age"}]},
      {"label": "Region", "columns": [{"type": "GROUP_CATEGORY", "field": "region"}]}
    ],
    "total": true,
    "overlays": [{"name": "sig", "kind": "OVERLAY_PAIRWISE_PROP_Z",
                  "scope": "column", "params": {"letters": {}}}]
  }
}
```

That request runs eight slots: `all:Total`, `all:Gender`, `all:Age`,
`all:Region`, `aware:Total`, `aware:Gender`, `aware:Age`,
`aware:Region`.

## Shape

- `request` is the base every slot starts from: the cohort, weight,
  filterers, `return` and the shared crosstab (rows, cell or cells,
  margins, normalize). It may not set `label`, crosstab `columns` (the
  groups own them) or crosstab `shape: long` (the stitch reads the
  matrix). Its `labels` bindings are scoped per slot: a binding rides
  only the slots whose crosstab rows or columns group by its field, so a
  `region` binding labels the Region columns without warning
  `PULSE_LABEL_NOT_APPLIED` on the Total, Gender and Age slots, and the
  stitched table carries the display labels. A binding that no slot
  groups by stays on every slot and warns there, as on a hand-written
  slot (its warning `index` is the slot's own `labels` position).
- `sections` (at least one) each carry a unique `label` and may append
  their own `filterers` after the base's — so KPIs with different bases
  (all adults, the aided-aware) share one table — and replace the base
  crosstab's `rows` and `cell` / `cells` (several cells are several
  measures as stub rows).
- `groups` each carry a unique `label` and at least one column grouper.
  They may be empty only when `total` is set.
- `total: true` adds the Total block, a crosstab with no column grouper
  (its one implicit Total column), stitched first. No group may then be
  labelled `Total`.
- `overlays` are request overlays appended to every GROUP slot after the
  base's own. The Total slot never receives them.
- A section may set its own `overlays`. When non-empty they REPLACE the
  banner-level `overlays` on that section's group slots — they do not
  add to them — so a mean stub can run
  `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` while the proportion stubs run
  `OVERLAY_PAIRWISE_PROP_Z`. The base request's overlays still lead
  every slot, and the Total slot still receives neither. A section that
  sets none inherits the banner-level list; there is no way to opt a
  section out of the banner-level overlays except by giving it its own.

Structural faults are `PULSE_BANNER_INVALID`, with `details.field` and
`details.reason`. Everything else — an unknown field, a bad grouper, an
overlay that does not fit the host — is judged on the expanded slot,
exactly as on its hand-written twin.

## The stitched table

`Banner.Sections[i]` holds one table per section. `RowKeys` is the union
of the blocks' rows, in first-seen order; `ColumnKeys` concatenates
every block's keys; `Cells[row][column]` and `ColumnMargins` are copied
from the slots. `Blocks` locates each block (`Start`, `Count`) and
carries its `ColumnHeader`, `RowMargins`, `GrandTotal` and, on a
multi-measure table, `Measures`. A record reaches a block's margin only
if it reached a CELL of that block, so row margins and grand totals are
per block. Nothing is recomputed: every figure equals its slot's.

## Letters

A banner overlay with `params.letters` adds `Letters` to each section:
one entry per such overlay, its cells on the stitched coordinates. A
section with its own overlays letters from those instead: its entries
carry `Section: true`, and `Overlay` indexes the section's `overlays`
rather than the banner's. Under
`column` scope the letters run on across the banner (Gender A and B, Age
C to E, Region F to I) and every block keeps its own comparison groups,
so a column is only ever compared with columns of its own group. The
Total block is compared to nothing: it never receives the banner
overlays, so its columns carry no letter. Under `row` scope the cells are
copied unchanged. A batch multiplicity block with family `compose` pools
every banner slot's tests (a section's own overlays in place of the
banner-level ones it replaced), and the letters are recomputed after
that fold.

## Limits, predict, return and echo

`MaxComposeSlots` counts explicit, sweep and banner slots before any slot
runs ([Tuning Limits](tuning-limits.md)). `PredictCompose` expands the
banner the same way and reports a `banner` summary: `sections`,
`groups`, `total`, `expanded_count` and `labels`. A banner slot always
computes its full response, because the stitch reads it unshaped; the
slot's `return` still trims it on the wire. `Banner` sits outside the
Compose-level `return`, so it is always emitted and never rounded.
`EchoRequest` echoes the expanded slots, and `--stream` ends with one
`{"banner": {...}}` line. The MCP default `return` fills the base request
when it sets none.

## Cost

A banner scans the cohort once per slot. On an Apple M1 Max, with
`ComposeParallel` and default workers over an in-memory cohort
(`BenchmarkComposeBanner`), one Total-only slot took 29 ms at 100,000
rows and 289 ms at 1,000,000 rows. A 1 × 4 banner (Total plus three
groups) took 47 ms and 442 ms, 1.5–1.6 times one slot. A 3 × 4 banner
(12 slots) took 116 ms and 964 ms, 3.3–4.0 times one slot. Parallel
slots overlap, so the cost grows well below the slot count.

See also: [Compose Sweeps](compose-sweeps.md), [Parallel
Compose](parallel-compose.md), the
[payload schema](../contract/payload-schema.md#compose-banner).
