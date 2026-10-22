```yaml
name: overlay-system
description: Overlay framework — OverlaySpec composition, reference families, three payload shapes, host-arm wiring (SERIES / MATRIX_RESULT / FACET / CHAIN / FORMULA), named payload parts of multi-output kinds. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
```

# Overlays

Additive, read-only decorations for a figure DERIVED from a result. Specs ride `Request.Overlays`, one layer each in `Response.Overlays[i]`, in spec order, keyed to host coordinates — the base payload is NEVER mutated.

## Which skill answers what

| Question | Skill |
|---|---|
| Pairwise kinds: p grid, `letters`, skips, weights | [`overlay-pairwise`](overlay-pairwise.md) |
| A pairwise leg's `n_source` / `p_source` | [`pairwise-n-sources`](pairwise-n-sources.md) |
| The Compose panel's n modes | [`panel-n-sources`](panel-n-sources.md) |
| Parity and prior-period tests | [`overlay-reference-tests`](overlay-reference-tests.md) |
| What a formula can read | [`overlay-formula-namespace`](overlay-formula-namespace.md) |

## Choosing an overlay

1. Name the question, then read its intent (`pulse_skills_get intents`).
2. Filter manifest `overlays[]` on that intent, then on the HOST you have — each entry lists `shapes`, `scopes`, `ref_kinds`, `buffered` and `inferential`.
3. Read the kind's atomic skill. Never hardcode a kind count.

## OverlaySpec composition

`kind`, `scope`, `ref`, optional `name` / `level` / `within` / `params`; Compose-only `reference` / `targets`. `scope` ∈ `cell|row|column|group|matrix|total`. `level` / `within` mirror the crosstab's `normalize_level` / `normalize_within`.

Predict and runtime refuse a misshapen spec, or a kind off its hosts, with the SAME `errors[0].code` (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`, …).

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

Implicit-margin kinds (χ² / Fisher cell tests) leave `ref` empty.

## Three payload shapes

`payload.shape` ∈ `scalar|series|matrix`. Scalar — `payload.scalar` + optional `summary`. Series — `payload.series.entries[i].{key, summary}`, aligned to host keys; the figure is `summary.statistic`. Matrix — `payload.matrix.cells[r][c]`, mirroring host cells. `baseline` is the centre point (100 for an index, 0 for a delta / z), absent for inferential kinds.

## Parts (multi-output kinds)

A multi-output kind (pairwise under `params.letters`) adds `payload.parts`: named plain scalar | series | matrix payloads beside the primary, never nested; manifest `overlays[].parts` lists them (`when` = condition). A points × dimensions part keys its columns `["dim_1"]`, `["dim_2"]`, …. Select one via `return`: `overlays[*].payload.parts.<part>`.

## Host-arm wiring

The carrying request decides the host, the host the legal kinds (an ungrouped or aggregation-free request runs none):

- **MATRIX** — a crosstab (`Request.Crosstab` + `Request.Overlays`): share, margin compare, cell tests, intra-matrix pairwise.
- **SERIES** — a grouped Process with no crosstab: per-group self-compare (sibling, baseline, prior, rolling, year-over-year). Over N ≥ 2 `groups` it runs per partition (`params.partition_by`, a leading key prefix; default all but the last). Scores `params.field`, else `aggregations[0]`.
- **MATRIX_RESULT** — `Request.Matrices` with no crosstab, on a process request or Compose slot (not chain, facet or Compose-host slots): reads every group bucket of the spec `ref.matrix` names. Crosstab + `matrices` is refused.
- **FACET** — `FacetRequest.Overlays`: population comparisons, never weighted (a set `weight` ⇒ `PULSE_WEIGHT_UNSUPPORTED`), layers on `FacetResult.Overlays` ([`facet-design`](facet-design.md)).
- **CHAIN** — whole-chain `ChainRequest.Overlays` against an earlier stage; layers on the chain response, per-stage overlays untouched. Stages of divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` ([`process-chain`](process-chain.md)).
- **FORMULA** — an expression over host figures (crosstab or grouped Process; any shape on Compose).
- **COMPOSE** — the post-slot fold compares slots (`reference` vs `targets`); slots must align on keys, schema and dictionaries. Layers on `ComposedResponse.Overlays[i]` (top-level `return` prunes post-fold), buffered only ([`compose-requests`](compose-requests.md)).

## Per-layer warnings (`OverlayLayer.Warnings`)

Lowercase `warnings: [{code, message, details}]` per layer, `omitempty`. Canonical: `PULSE_OVERLAY_REF_ZERO` (zero reference), `PULSE_OVERLAY_EXPECTED_LOW` (expected count below 5: χ² cells, symmetry pairs). On Compose and chain hosts it rides the matching layer's `Overlays[i].Warnings` (banner: each slot's own layers, never `banner`); a partitioned SERIES layer raises each code once (`details.partitions`). Warnings never fail the request.

## Multiplicity

Inferential layers take `multiplicity`: adds `p_adjusted` figures and a layer echo; pairwise letters follow it ([`multiplicity-correction`](multiplicity-correction.md)).

## Streamability

Manifest `overlays[].buffered`; descriptive SERIES kinds stream. MATRIX kinds fold after the crosstab finishes, so they never decide whether it fuses ([`crosstab-guide`](crosstab-guide.md)). One buffered kind prices the request buffered.

## Weighted hosts

Descriptive kinds read the weighted payload. Inferential kinds RUN on each cell's floor-key N* (Σw, or Kish n_eff under `probability`; echoed in `summary.parameters`). The few kinds that refuse a weight (`PULSE_WEIGHT_UNSUPPORTED`) say so in their `## Contract`. Spec `"weight": null` opts the slot out, not the host cell ([`weighting`](weighting.md)).

## See

- [`overlay-pairwise`](overlay-pairwise.md), [`pairwise-n-sources`](pairwise-n-sources.md), [`panel-n-sources`](panel-n-sources.md), [`overlay-reference-tests`](overlay-reference-tests.md), [`overlay-formula-namespace`](overlay-formula-namespace.md), [`response-components`](response-components.md), [`crosstab-guide`](crosstab-guide.md), [`facet-design`](facet-design.md), [`compose-requests`](compose-requests.md), [`process-chain`](process-chain.md), [`multiplicity-correction`](multiplicity-correction.md).
