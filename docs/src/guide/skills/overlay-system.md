```yaml
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), named payload parts of multi-output kinds. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
```

# Overlays

Additive, read-only decorations on a result. Specs ride `Request.Overlays`; each produces one layer in `Response.Overlays[i]`, in spec order. Overlays NEVER mutate the base payload — they are siblings keyed to host coordinates, so base numbers stay byte-identical.

Use an overlay for a figure DERIVED from the result — a share, an index against a reference, a delta, a z-score, a cell test.

```jsonc
{"overlays": [{"kind": "<overlay kind>", "scope": "cell", "ref": {"margin": {"axis": "row"}}}]}
```

## Choosing an overlay

1. Name the question, then read the matching intent (`pulse_skills_get intents`): `composition` (shares), `benchmark` (index / delta against a reference), `compare_groups` (cell and pairwise tests), `change_over_time` (prior period, rolling, baseline).
2. Filter manifest `overlays[]` on that intent, then on the HOST you have — each entry lists `shapes`, `scopes`, `ref_kinds`, `buffered` and `inferential`.
3. Read the kind's atomic skill for math, params and warnings. The authoritative catalog is `overlays[]` itself; never hardcode a count.

## OverlaySpec composition

`kind`, `scope`, `ref`, optional `name` / `level` / `within` / `params`; Compose-only `reference` / `targets`. `scope` ∈ `cell|row|column|group|matrix|total`. `level` / `within` mirror the crosstab's `normalize_level` / `normalize_within` (same-axis rollup, opposite-axis prefix).

Predict and runtime refuse a misshapen spec — or a kind on a slot or shape it does not run on — under the SAME code (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`, …), carried as `errors[0].code` so `pulse_errors_lookup` resolves it.

## Six reference families

`ref` is a discriminated union — exactly one family populated:

| Family | Compares against | Host |
|---|---|---|
| `margin{axis}` | the row / column / grand margin | crosstab |
| `sibling{field, value}` | another group's value | grouped Process |
| `baseline_index{position}`, `prior`, `rolling_mean`, `yoy` | an earlier point in the same series | grouped Process |
| `population{cohort}` | a reference cohort | facet |
| `stage{index or name}` | an earlier chain stage | process chain |
| `reference` + `targets` (spec top level) | another Compose slot | compose |

Implicit-margin kinds (the χ² and Fisher cell tests) leave `ref` empty.

## Three payload shapes

`payload.shape` ∈ `scalar|series|matrix`. Scalar — `payload.scalar` + optional `summary`. Series — `payload.series.entries[i].{key, summary}`, aligned to host keys; the figure is `summary.statistic`. Matrix — `payload.matrix.cells[r][c]`, mirroring host cells. `baseline` is the centre point (100 for an index, 0 for a delta / z) and is absent for inferential kinds (manifest `inferential: true`).

## Parts (multi-output kinds)

A multi-output kind adds `payload.parts`: named secondary payloads beside the primary. Each is a plain scalar | series | matrix payload, never nested; the kind's skill names them. A points × dimensions part keys its columns `["dim_1"]`, `["dim_2"]`, …. Select one via `return`: `overlays[*].payload.parts.<part>`.

## Host-arm wiring

The carrying request decides the host; the host decides the legal kinds:

- **MATRIX** — a crosstab (`Request.Crosstab` + `Request.Overlays`): share, margin compare, cell tests, intra-matrix pairwise. Pairwise n: [`pairwise-n-sources`](pairwise-n-sources.md).
- **SERIES** — a grouped Process with no crosstab: per-group self-compare (sibling, baseline, prior, rolling, year-over-year). Over N ≥ 2 `groups` it runs per partition: `params.partition_by`, a leading key prefix (default all but the last; `[]` none); YoY: every non-date key. Scores the aggregation `params.field` labels; absent, `aggregations[0]`.
- **FACET** — `FacetRequest.Overlays`: population comparisons, layers on `FacetResult.Overlays` ([`facet-design`](facet-design.md)).
- **CHAIN** — whole-chain `ChainRequest.Overlays` against an earlier stage; layers on the chain response, per-stage overlays untouched. Stages of divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` ([`process-chain`](process-chain.md)).
- **FORMULA** — an expression over host figures: a crosstab on `Request.Overlays`, any shape on Compose.
- **COMPOSE** — the post-slot fold compares slots (`reference` vs `targets`); slots must align on keys, schema and dictionaries. Layers on `ComposedResponse.Overlays[i]` (top-level `return` prunes post-fold), buffered only ([`compose-requests`](compose-requests.md)).

## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `warnings: [{code, message, details}]` on each layer, `omitempty` — overlay-free responses stay byte-identical. Canonical: `PULSE_OVERLAY_REF_ZERO` (zero reference, undefined ratio), `PULSE_OVERLAY_EXPECTED_LOW` (χ² expected count below 5). A warning lands on the layer that raised it; on Compose and chain hosts it rides `Overlays[i].Warnings` of the matching layer. A partitioned SERIES layer raises each code once (`details.partitions`). Warnings never fail the request — read them before reporting a figure.

## Multiplicity

Inferential layers take `multiplicity` (`layer` / `row` / `column` / `request` / `compose`): adds `p_adjusted` figures and a layer echo, base payload untouched ([`multiplicity-correction`](multiplicity-correction.md)).

## Streamability

Manifest `overlays[].buffered`. Descriptive SERIES kinds stream; inferential kinds buffer. MATRIX kinds fold after the matrix is finished, so an overlay never decides whether a crosstab fuses — the cell aggregator does ([`crosstab-guide`](crosstab-guide.md)). Mixing streamable and buffered kinds prices the request as buffered.

## Weighted hosts

Descriptive kinds (shares, indices, deltas) read the host's weighted figures as-is. An inferential kind refuses a weight that reaches it (request, instance default, its own `weight`) with `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` on the spec opts out.

## See

- [`response-components`](response-components.md), [`crosstab-guide`](crosstab-guide.md), [`facet-design`](facet-design.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`compose-requests`](compose-requests.md), [`process-chain`](process-chain.md).
