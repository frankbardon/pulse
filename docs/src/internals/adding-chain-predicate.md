# Adding a Chain-Stage Predicate

**Audience:** Pulse internals contributors tightening or relaxing the
gate that decides whether an operator is allowed in a `ProcessChain`
stage.

`ProcessChain` (`pulse.ProcessChain`, `pulse_process_chain`,
`pulse api process-chain`) executes a linear pipeline whose stages all
pass one shared gate, `internal/mergegate.ChainRefusal`. The gate enforces that each stage
emits a shape the next stage can consume; v1 admits mergeable operators
only, and every mergeable built-in aggregator emits one `float64` per row.

## 1. Edit the shared gate

Edit `internal/mergegate/gate.go`. `ChainRefusal` runs `MergeRefusal`
(the merge rule `processing.CanMergeRequestWithExtensions` also
delegates to) and, today, nothing else — no mergeable built-in emits a
shape the next stage cannot read. Add a chain-specific branch after
`MergeRefusal` when an operator is mergeable but its emit shape would
break the synthesised `f64` / `categorical_u32` schema the next stage
expects; return a reason string naming the operator.

## 2. Both sides pick it up

There is nothing to mirror. The runtime calls it as
`processing.ChainRefusal` (over the `ExtensionRegistry`) and
`internal/descriptor/chain.go` calls `mergegate.ChainRefusal` directly
(over the `ExtensionsSnapshot`), so code, message and details
`{stage_index, stage_name}` are identical by construction. The package
imports only `types`, `encoding` and `errors`: a fact only the engine
holds must reach it through the `mergegate.Extensions` interface, which
both adapters implement — never by importing `internal/processing`.

## 3. Update the capability surface

Edit `internal/descriptor/capabilities_chain.go`. `processChainCapability()`
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

Add the rule's case to `internal/mergegate/gate_test.go` and a row to
`TestChainGate_ValidatorMatchesRuntime` (root `chain_gate_parity_test.go`),
which runs each stage through `ProcessChain` and `ValidateChainWithOptions`
and requires the same code, message and details.

## 5. Allowlist skim

Skim `skills/session-bootstrap.md` and `skills/process-chain.md` for
any operator allowlist that needs adjustment in prose.

## 6. Whole-chain overlays

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
to the overlay-free baseline; populated overlays fold into the hash
automatically. Locked by `TestChainCanonicalHash_OverlayFreeByteIdentity`
and `TestChainCanonicalHash_OverlaysIncluded`.

Whole-chain handler dispatch lives in `internal/processing/overlay_chain_dispatch.go`.
Predict-time validation lives in `internal/descriptor/chain_overlay.go` —
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
go test ./internal/service/ -run TestChain
go test ./internal/descriptor/ -run 'TestValidateChain|TestProcessChain'
go test ./descriptor/ -run TestGoldensNotHandEdited
```

The Update Demand row for `ProcessChain` capability changes covers
all of these in one PR; see [The Update Demand](update-demand.md).
