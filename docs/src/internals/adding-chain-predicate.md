# Adding a Chain-Stage Predicate

**Audience:** Pulse internals contributors tightening or relaxing the
gate that decides whether an operator is allowed in a `ProcessChain`
stage.

`ProcessChain` (`pulse.ProcessChain`, `pulse_process_chain`,
`pulse api process-chain`) executes a linear pipeline whose stages all
pass `processing.CanChainRequest`. The gate enforces that each stage
emits a shape the next stage can consume; v1 admits mergeable scalar-
emitting operators only.

## 1. Edit the runtime gate

Edit `processing/chain.go`. `CanChainRequest` calls `CanMergeRequest`
first, then layers chain-specific exclusions
(`aggregatorEmitsScalar`). Add a new exclusion branch when an
operator is mergeable but its emit shape would break the synthesised
`f64` / `categorical_u32` schema the next stage expects.

## 2. Mirror in the predict gate

Mirror the rule in `descriptor/chain.go`. `chainGateOK` is the
predict-side equivalent — keep them in lockstep. A divergence makes
predict pass requests that runtime later rejects, which surfaces as a
late `SERVICE_VALIDATION` rather than as a predict-time advisory.

## 3. Update the capability surface

Edit `descriptor/capabilities_chain.go`. `processChainCapability()`
carries the manifest-facing allowlists and `RejectionRules` strings.
After editing, regenerate `descriptor/testdata/manifest.json`:

```bash
go test ./descriptor/ -update -run Golden
```

Then verify the new hash sticks:

```bash
go test ./descriptor/ -run TestGoldensNotHandEdited
```

## 4. Tests

Add a failing-gate test in `service/chain_test.go` and a matching
predict test in `descriptor/chain_test.go`. The two surfaces share
the gate contract; covering both prevents the predict / runtime
divergence the rule exists to prevent.

## 5. Allowlist skim

Skim `skills/getting-started.md` and `skills/mcp-integration.md` for
any operator allowlist that needs adjustment in prose.

## 6. Whole-chain overlays (E6)

`ChainRequest.Overlays []*ChainOverlaySpec` is the whole-chain overlay
surface — overlays here execute AFTER every stage finalises (NOT
between stages). Per-stage overlays continue to ride the universal
`ChainStage.Request.Overlays []OverlaySpec` slot.

- `ChainOverlaySpec` (in `types/chain.go`): `Name string`,
  `Kind OverlayKind`, `Ref StageRef`, `Target StageRef`,
  `Scope OverlayScope`, `Params map[string]any`.
- `StageRef` (in `types/chain.go`): XOR `{Index *int, Name string}`.
  `Index` is a pointer so `0` is meaningful — the canonical "stage 0"
  call site sets `Index = &zero`, not `Index` unset. The downstream
  validator enforces "exactly one of Index / Name".
- `OverlayRef.Stage` (in `types/overlay.go`) is the same `*StageRef`
  — there is exactly one `StageRef` declaration in the codebase. The
  legacy `OverlayStageRef` identifier is a type alias to `StageRef`
  and is deprecated.
- `ChainResponse.Overlays []*OverlayLayer` reuses the universal
  `OverlayLayer` wrapper from `types/overlay.go` (one entry per
  `ChainRequest.Overlays` spec in matching index order).

Canonical-hash coverage is data-driven (`types/hash.go`): the slot's
`omitempty` tag means overlay-free chain requests hash byte-identically
to the pre-E6-S2 form; populated overlays fold into the hash
automatically. Locked by `TestChainCanonicalHash_OverlayFreeByteIdentity`
and `TestChainCanonicalHash_OverlaysIncluded`.

Whole-chain handler dispatch lives in `processing/overlay_chain_dispatch.go`.
Predict-time validation lives in `descriptor/chain_overlay.go` —
`ValidateChain` walks `ChainRequest.Overlays` after the per-stage
gate and emits:

- `PULSE_OVERLAY_KIND_UNKNOWN` — anything outside
  `OVERLAY_INDEX_VS_STAGE` / `OVERLAY_DELTA_VS_STAGE` is rejected
  today.
- `PULSE_OVERLAY_REFERENCE_UNKNOWN` /
  `PULSE_OVERLAY_TARGET_UNKNOWN` — StageRef resolution failures.
  Same Index / Name / XOR / latest-stage-default contract the runtime
  `resolveChainStageRef` uses.
- `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` — Ref and Target
  stages produce different host shapes per `inferChainStageShape`:
  `req.Crosstab != nil ⇒ MATRIX`, `req.Aggregations + req.Groups ⇒ SERIES`,
  `req.Aggregations only ⇒ SCALAR`.

Every rejected `(Ref, Target)` pair also lands in
`ChainValidationResult.OverlaysSchemaDivergence` so LLM planners can
budget reshapes without re-parsing envelope details.

## 7. Run the gates

```bash
go test ./service/ -run TestChain
go test ./descriptor/ -run 'TestValidateChain|TestProcessChain'
go test ./descriptor/ -run TestGoldensNotHandEdited
```

The Update Demand row for `ProcessChain` capability changes covers
all of these in one PR; see [The Update Demand](update-demand.md).
