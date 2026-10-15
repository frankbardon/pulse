---
name: overlay-system
description: Overlay framework — OverlaySpec composition, reference families, three payload shapes, host-arm wiring (SERIES / MATRIX_RESULT / FACET / CHAIN / FORMULA), named payload parts of multi-output kinds. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations on a result. Specs ride `Request.Overlays`; each produces one layer in `Response.Overlays[i]`, in spec order. Overlays NEVER mutate the base payload — they are siblings keyed to host coordinates, so base numbers stay byte-identical.

Use one for a figure DERIVED from the result: a share, index, delta, z-score, cell test.

```jsonc
{"overlays": [{"kind": "<overlay kind>", "scope": "cell", "ref": {"margin": {"axis": "row"}}}]}
```

## Choosing an overlay

1. Name the question, then read its intent (`pulse_skills_get intents`): `composition`, `benchmark`, `compare_groups`, `change_over_time`.
2. Filter manifest `overlays[]` on that intent, then on the HOST you have — each entry lists `shapes`, `scopes`, `ref_kinds`, `buffered` and `inferential`.
3. Read the kind's atomic skill for math, params and warnings. Never hardcode a kind count.

## OverlaySpec composition

`kind`, `scope`, `ref`, optional `name` / `level` / `within` / `params`; Compose-only `reference` / `targets`. `scope` ∈ `cell|row|column|group|matrix|total`. `level` / `within` mirror the crosstab's `normalize_level` / `normalize_within` (same-axis rollup, opposite-axis prefix).

Predict and runtime refuse a misshapen spec — or a kind on a slot or shape it does not run on — under the SAME code (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`, `_FORMULA_INVALID_IDENT`, …), carried as `errors[0].code` so `pulse_errors_lookup` resolves it.

## Reference families

`ref` is a discriminated union — exactly one family populated:

| Family | Compares against | Host |
|---|---|---|
| `margin{axis}` | the row / column / grand margin | crosstab |
| `sibling{field, value}` | another group's value | grouped Process |
| `baseline_index{position}`, `prior`, `rolling_mean`, `yoy` | an earlier point in the same series | grouped Process |
| `population{cohort}` | a reference cohort | facet |
| `stage{index or name}` | an earlier chain stage | process chain |
| `matrix{name}` | one matrix spec's result, every group bucket | matrix result |
| `reference` + `targets` (spec top level) | another Compose slot | compose |

Implicit-margin kinds (the χ² and Fisher cell tests) leave `ref` empty.

## Three payload shapes

`payload.shape` ∈ `scalar|series|matrix`. Scalar — `payload.scalar` + optional `summary`. Series — `payload.series.entries[i].{key, summary}`, aligned to host keys; the figure is `summary.statistic`. Matrix — `payload.matrix.cells[r][c]`, mirroring host cells. `baseline` is the centre point (100 for an index, 0 for a delta / z) and is absent for inferential kinds (manifest `inferential: true`).

## Parts (multi-output kinds)

A multi-output kind adds `payload.parts`: named secondary payloads beside the primary. Each is a plain scalar | series | matrix payload, never nested; manifest `overlays[].parts` lists them (`when` = condition). A points × dimensions part keys its columns `["dim_1"]`, `["dim_2"]`, …. Select one via `return`: `overlays[*].payload.parts.<part>`.

## Host-arm wiring

The carrying request decides the host; the host decides the legal kinds (an ungrouped or aggregation-free request runs none — every spec is refused):

- **MATRIX** — a crosstab (`Request.Crosstab` + `Request.Overlays`): share, margin compare, cell tests, intra-matrix pairwise.<!-- feature: capability:crosstab --> Pairwise n: `pairwise-n-sources`.<!-- /feature -->
- **SERIES** — a grouped Process with no crosstab: per-group self-compare (sibling, baseline, prior, rolling, year-over-year). Over N ≥ 2 `groups` it runs per partition (`params.partition_by`, a leading key prefix; default all but the last). Scores `params.field`, else `aggregations[0]`.
<!-- feature: capability:matrices -->- **MATRIX_RESULT** — `Request.Matrices` with no crosstab, on a process request or a Compose slot (never the chain, facet or Compose-host slots): reads every group bucket of the spec `ref.matrix` names. Crosstab + `matrices` is refused.<!-- /feature -->
- **FACET** — `FacetRequest.Overlays`: population comparisons, layers on `FacetResult.Overlays`<!-- feature: capability:facet --> (`facet-design`)<!-- /feature -->.
- **CHAIN** — whole-chain `ChainRequest.Overlays` against an earlier stage; layers on the chain response, per-stage overlays untouched. Stages of divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`<!-- feature: capability:process_chain --> (`process-chain`)<!-- /feature -->.
- **FORMULA** — an expression over host figures: a crosstab or grouped Process on `Request.Overlays`, any shape on Compose.
- **COMPOSE** — the post-slot fold compares slots (`reference` vs `targets`); slots must align on keys, schema and dictionaries. Layers on `ComposedResponse.Overlays[i]` (top-level `return` prunes post-fold), buffered only<!-- feature: capability:compose --> (`compose-requests`)<!-- /feature -->.

## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `warnings: [{code, message, details}]` on each layer, `omitempty` — overlay-free responses stay byte-identical. Canonical: `PULSE_OVERLAY_REF_ZERO` (zero reference, undefined ratio), `PULSE_OVERLAY_EXPECTED_LOW` (expected count below 5: χ² cells, symmetry pairs). On Compose and chain hosts it rides the matching layer's `Overlays[i].Warnings`; a partitioned SERIES layer raises each code once (`details.partitions`). Warnings never fail the request — read them before reporting a figure.

<!-- feature: capability:multiplicity -->
## Multiplicity

Inferential layers take `multiplicity` (`layer` / `row` / `column` / `request` / `compose`): adds `p_adjusted` figures and a layer echo, base payload untouched (`multiplicity-correction`).
<!-- /feature -->

## Streamability

Manifest `overlays[].buffered`; descriptive SERIES kinds stream. MATRIX kinds fold after the crosstab finishes, so they never decide whether it fuses<!-- feature: capability:crosstab --> (`crosstab-guide`)<!-- /feature -->. One buffered kind prices the request buffered.

## Weighted hosts

Descriptive kinds read the host's weighted figures as-is. An inferential kind refuses a weight that reaches it with `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` on the spec opts out.

## See

- `response-components`<!-- feature: capability:crosstab -->, `crosstab-guide`<!-- /feature --><!-- feature: capability:facet -->, `facet-design`<!-- /feature --><!-- feature: capability:crosstab -->, `pairwise-n-sources`<!-- /feature --><!-- feature: capability:compose -->, `compose-requests`<!-- /feature --><!-- feature: capability:process_chain -->, `process-chain`<!-- /feature -->.
