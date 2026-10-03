---
name: overlay-system
description: Overlay framework — OverlaySpec composition, six reference families, three payload shapes, host-arm wiring (SERIES / FACET / CHAIN / FORMULA), parity overlays + Welford migration. Per-kind detail lives in op-overlay-* atomics.
type: guide
kind: design
applies_to: process, compose, facet
covers: [OVERLAY, OverlaySpec, OverlayLayer]
---

# Overlays

Additive, read-only decorations on a result that is already computed. Specs ride `Request.Overlays`; each produces one layer in `Response.Overlays[i]`, in spec order. Overlays NEVER mutate the base payload — they are siblings keyed to host coordinates, so the base numbers are byte-identical with or without them.

Use an overlay for a figure DERIVED from the result — a share, an index against a reference, a delta, a z-score, a significance test between cells. Base figures stay aggregations.

```jsonc
{"overlays": [{"kind": "<overlay kind>", "scope": "cell", "ref": {"margin": {"axis": "row"}}}]}
```

## Choosing an overlay

1. Name the question, then read the matching intent (`pulse_skills_get intents`): `composition` (shares), `benchmark` (index / delta against a reference), `compare_groups` (cell and pairwise tests), `change_over_time` (prior period, rolling, baseline).
2. Filter manifest `overlays[]` on that intent, then on the HOST you have — each entry lists `shapes`, `scopes`, `ref_kinds`, `buffered` and `inferential`.
3. Read the chosen kind's atomic skill for its math, params and warnings. The authoritative catalog is `overlays[]` itself; never hardcode a count.

## OverlaySpec composition

`kind`, `scope`, `ref`, optional `name` / `level` / `within` / `params`; Compose-only `reference` / `targets`. `scope` ∈ `cell|row|column|group|matrix|total`. `level` / `within` mirror the crosstab's `normalize_level` / `normalize_within` (same-axis rollup, opposite-axis prefix).

Predict and runtime refuse a misshapen spec under the SAME code (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `_LEVEL_OUT_OF_RANGE`, `_SCOPE_UNSUPPORTED`, …). A fault always carries its real `PULSE_OVERLAY_*` code as `errors[0].code`, so `pulse_errors_lookup` resolves it; `PROCESSING_INTERNAL` means a caller-side invariant break (nil spec / host), never a user error.

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

`payload.shape` ∈ `scalar|series|matrix`. Scalar — `payload.scalar` + optional `summary{statistic, p_value, parameters}`. Series — `payload.series.entries[i].{key, value, summary}`, aligned to host keys. Matrix — `payload.matrix.cells[r][c]`, mirroring host cells. `baseline` is the centre point (100 for an index, 0 for a delta / z) and is absent for inferential kinds (manifest `inferential: true`).

## Host-arm wiring

Which request carries the spec decides the host, and the host decides which kinds are legal:

- **MATRIX** — a crosstab (`Request.Crosstab` + `Request.Overlays`): share, margin compare, cell tests, intra-matrix pairwise. Pairwise sample-size sources: `pairwise-n-sources`.
- **SERIES** — a grouped Process with no crosstab: per-group self-compare (sibling, baseline, prior, rolling, year-over-year).
- **FACET** — `FacetRequest.Overlays` (NOT `Request.Overlays`): population comparisons, layers on `FacetResult.Overlays` (`facet-design`).
- **CHAIN** — whole-chain `ChainRequest.Overlays` against an earlier stage; layers on the chain response, per-stage overlays untouched. Stages of divergent shape ⇒ `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` (`process-chain`).
- **FORMULA** — an expression over earlier layers, referenced by `name`.
- **COMPOSE** — the Compose post-slot fold compares slots (`reference` vs `targets`) after every slot ran; slots must align on label, keys, schema and dictionaries. Layers on `ComposedResponse.Overlays[i]`, buffered only (`compose-requests`).

## Per-layer warnings (`OverlayLayer.Warnings`)

Additive `warnings: [{code, message, details}]` on each layer, `omitempty` — overlay-free responses stay byte-identical. Canonical: `PULSE_OVERLAY_REF_ZERO` (a reference value of zero, so the ratio is undefined), `PULSE_OVERLAY_EXPECTED_LOW` (χ² expected count below 5). A warning lands on the layer that raised it; on Compose and chain hosts it rides `Overlays[i].Warnings` of the matching layer. Warnings never fail the request — read them before reporting a figure.

## Streamability

Manifest `overlays[].buffered`. Descriptive SERIES kinds stream; inferential kinds buffer. MATRIX kinds fold after the matrix is finished, so an overlay never decides whether a crosstab fuses — the cell aggregator does (`crosstab-guide`). Mixing streamable and buffered kinds on one request prices it as buffered.

## Parity overlays — Welford migration

The cell t / z kinds and their Compose vs-ref twins read `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]`, so the cell aggregator must emit that triple; with no triple at the coordinate they fall back to `params` (`variance_*`, `sample_size_*`). The cell's own `MatrixCell.Value` is the scalar mean. P-values equal the matching row-level two-sample tests on the same inputs.

## See

- `response-components`, `crosstab-guide`, `facet-design`, `pairwise-n-sources`, `compose-requests`, `process-chain`.
