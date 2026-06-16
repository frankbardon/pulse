---
name: process-chain
description: ChainRequest source-rooted linear pipeline — mergeable-only v1 gate, per-stage Response.Components, dual-slot overlays (per-stage + whole-chain), StageRef resolution, shape-divergence warning.
type: guide
kind: design
applies_to: process-chain, predict
covers: [ChainRequest, pulse_process_chain, OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT, OVERLAY_DELTA_VS_STAGE, OVERLAY_INDEX_VS_STAGE]
---

# Process chain

`ChainRequest` runs a linear pipeline rooted at one cohort: stage 0 reads the source; each later stage consumes the synthesised cohort from its predecessor. CLI `pulse api process-chain`; library `pulse.ProcessChain`; MCP `pulse_process_chain`.

```jsonc
{
  "cohort": {"path": "sales-2025.pulse"},
  "stages": [
    {"name": "by_region",
     "request": {"aggregations": [{"type": "AGG_SUM", "field": "revenue"}],
                 "groups":       [{"type": "GROUP_CATEGORY", "field": "region"}]}},
    {"name": "top3", "request": {"sort": {"field": "revenue", "limit": 3}}}
  ]
}
```

## Mergeable-only stage gate (v1)

Every stage must pass `processing.CanChainRequest` — calls `CanMergeRequest`, then layers chain-specific exclusions (`aggregatorEmitsScalar`). Failure surfaces `PULSE_CHAIN_NOT_MERGEABLE`. Predict-side: `descriptor.chainGateOK` — call `pulse predict --json` before pricing. `processChainCapability()` carries the manifest-facing allowlists + `RejectionRules`.

The constraint exists because the synthesised inter-stage cohort uses an f64 / categorical_u32 projection — operators whose emit shape can't be folded into it (`AGG_MEDIAN`, `GROUP_QUANTILE`, etc.) break the bridge.

## Per-stage `Response.Components` (v0.20.0)

Each stage emits its own `Response.Components` independently — universal floor (`{n, n_null}` aggregators, `{total_n, n_null}` groupers, `{n_in, n_out, n_null_input}` filterers) plus per-operator keys ride the per-stage `ChainResponse.Stages[i].Response.Components`. The chain overlay layer composes per-stage component reads but **does not mutate** them — overlays are additive siblings.

`_VS_STAGE` handlers read `Stages[ref].Response.Components` + `Stages[target].Response.Components` as inputs; no rewrite path exists.

## Dual-slot overlay design

Two independent overlay slots:

- **Per-stage** (`ChainStage.Request.Overlays []OverlaySpec`) — rides the universal `Request.Overlays`. Chain layer is transparent; each stage's handlers execute at the per-stage exit BEFORE the next stage receives its synthesised cohort. Lands on `ChainResponse.Stages[i].Overlays`. No chain-specific code.

- **Whole-chain** (`ChainRequest.Overlays []*ChainOverlaySpec`) — executes AFTER every stage finalises (NOT between stages). Two kinds: `OVERLAY_INDEX_VS_STAGE` (`target/ref * 100`), `OVERLAY_DELTA_VS_STAGE` (`target - ref`). Lands on `ChainResponse.Overlays` in declared order — NOT on `Stages[i].Overlays`.

```jsonc
{
  "cohort": {"path": "sales-2025.pulse"},
  "stages": [
    {"name": "raw",    "request": {"aggregations": [{"type": "AGG_SUM", "field": "revenue"}],
                                   "groups": [{"type": "GROUP_CATEGORY", "field": "region"}]}},
    {"name": "filter", "request": {"filterers": [{"type": "FILTER_INCLUDE", "field": "active", "values": ["true"]}]}},
    {"name": "final",  "request": {"sort": {"field": "revenue"}}}
  ],
  "overlays": [
    {"kind": "OVERLAY_INDEX_VS_STAGE", "ref": {"index": 0}, "target": {"index": 2}},
    {"kind": "OVERLAY_DELTA_VS_STAGE", "ref": {"name": "raw"}, "target": {"name": "final"}}
  ]
}
```

## `StageRef` resolution

`Ref` and `Target` accept exactly one of `Index *int` (zero-based pointer) or `Name string` (matches `ChainStage.Name`). Both populated OR both empty rejects with `PULSE_OVERLAY_*` configuration error. `Index` is a pointer so `0` is distinguishable from "no index supplied" — set `Index: &zero` for stage 0.

When `Target` is fully empty (`Index: nil` and `Name: ""`), the resolver defaults to latest stage (`len(Stages) - 1`). `Ref` has no default — every spec MUST name a baseline explicitly.

```jsonc
{"ref": {"index": 0},    "target": {"index": 2}}      // index form
{"ref": {"name": "raw"}, "target": {"name": "final"}} // name form
```

## Shape-divergence warning

`_VS_STAGE` overlays only fire when ref + target produce the same host shape. When they diverge (`Ref` is SERIES, `Target` is MATRIX), runtime emits **one** `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` warning per spec and surfaces an empty payload inheriting the target's shape — no-op, NOT fatal.

Shape inference (`inferChainStageShape`): `req.Crosstab != nil ⇒ MATRIX`; `Aggregations + Groups ⇒ SERIES`; `Aggregations` only ⇒ SCALAR. `descriptor.ValidateChain` walks `ChainRequest.Overlays` after the per-stage gate and emits:

- `PULSE_OVERLAY_KIND_UNKNOWN` — outside `OVERLAY_INDEX_VS_STAGE` / `OVERLAY_DELTA_VS_STAGE`.
- `PULSE_OVERLAY_REFERENCE_UNKNOWN` / `PULSE_OVERLAY_TARGET_UNKNOWN` — `StageRef` resolution failure (Index OOR, Name unmatched, XOR violated).
- `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` — host-shape mismatch.

Every rejected `(Ref, Target)` populates `ChainValidationResult.OverlaysSchemaDivergence`. Warning Details carry `{target_shape, ref_shape, target_index, ref_index}` to distinguish shape mismatch from `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Hashing & echo

`ChainRequest.Hash()` returns a 32-char canonical-JSON digest. `Overlays` is `omitempty` — overlay-free chains hash byte-identically to the pre-overlay form. `--echo-request` populates `envelope.request` with per-stage normalised echoes.

## See

- `overlay-system` — `_VS_STAGE` catalog, shared `indexKernel` / `deltaKernel`, shape-inheritance rules.
- `response-components` — per-stage Components shape + universal floor.
- `streaming-and-watching` — `ChainRequest.Hash()` cache keys.
- `contributor-workflow` — adding a chain-stage predicate; `processing/chain.go`, `descriptor/chain.go` edits.
