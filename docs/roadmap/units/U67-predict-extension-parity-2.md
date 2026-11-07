---
id: U67
slug: predict-extension-parity-2
title: "An extension or an unusual request is judged the same way by predict and by the runtime, with no runtime-only refusal left"
track: API & release
size: L
status: not-started
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [466, 475, 523, 524, 573, 559, 577, 244, 245, 486, 676, 479, 708]
branch: predict-extension-parity-2
---

# U67 — predict-extension-parity-2

**Outcome:** An extension or an unusual request is judged the same way by predict and by the runtime, with no runtime-only refusal left.

**Track:** API & release · **Size:** L · **Depends on:** U77, U78 (CI and test hygiene land first)

## Summary

The residue of predict / runtime parity and extension validation: extension factories and window-label reads that only the runtime can judge, a standalone public post-window classifier, `u64` binding past 2^53, a single expression-slot walk, extension attributes with output dictionaries, embedder-registered overlay kinds, and a general `target` / `predictors` refusal on non-regression attributes.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#466** Predict's `return` size estimate ignores the Compose slot `label` that `standard` / `minimal` now keep (a few bytes per slot; negligible, recorded so the estimate stays honest). Found in U44 (E3-S2). Done (E5-S4): Compose predict reports each slot's sizes (`ComposeSlotPredict.Sizes`, `slots[*].sizes`) — Process predict's figures for the same request under the batch's multiplicity, headed by an exact `label` section every preset keeps (`TestComposePredict_SlotSizesMatchProcessPredict`) · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#475** Optional `PULSE_ADVISORY_*` for an `ATTR_FORMULA` that can yield null without a guard: predict cannot know, so an advisory is a contract call (owner). Found in U47 (E4-S5) · [U67](units/U67-predict-extension-parity-2.md)
- [x] **#523** Residual `u64` float64 binding, exact only up to 2^53: an expression reading `$env`, a comparison routed through a `let` name, and a `u64` column rewritten by a feature or attribute. Bind the stored word on those paths or record the limit in the migration doc. Found in U58 (E3-S1). Done (E4-S1, E4-S2): a static `$env.id` / `$env?.id` read and a let bound to a u64 read bind the word; an `ATTR_FORMULA` that IS a bare u64 read is a formula alias (`prevstate.U64Aliases`) whose label binds the field's word in every later expression on every arm, predict declaring the same; no feature or attribute rewrites a `u64` column in place (shadow refused), and no other one writes a u64 word. A dynamic `$env` use and a computed column keep the echo (migration doc) · [U67](units/U67-predict-extension-parity-2.md)
- [x] **#524** `vectors.ExprSlots` (the engine reference walk) does not walk features, for example FEAT_TARGET_ENCODE `params.fit_on`; the template / sweep side covers it via `internal/exprslots`. Unify the two walks and add a parity test. Found in U58 (E4-S1). Done (E4-S3): `vectors.ExprSlots` walks each FEAT_TARGET_ENCODE `params.fit_on` (`vectors.FeatureFitOn`), so `mentionsRef`, `ReferenceIssues`, `StaticIssues`, `LengthIssues`, `MetricUnsuitedIssues` and `SweepSlotReadsFit` read it as they read an attribute's; `TestTemplateExprSplice_EngineExprSlotsMatch` is bidirectional against `internal/exprslots` with an explicit out-of-scope list (`holdout`, read beside ExprSlots by its owners). The two walks stay separate (typed vs JSON key path, exprslots stdlib-only). · [U67](units/U67-predict-extension-parity-2.md)
- [x] **#573** A standalone public post-window classifier: predict's `post_window` block needs a cohort header or a schema, so a caller with no cohort cannot classify a draft. Deferred from U63. Done (E5-S3): `(*pulse.Pulse).ClassifyPostWindow(ctx, req, schema)` returns Predict's `post_window` for the draft over a cohort with that schema (instance routes, extension hooks, defaults, loaded `models` / `populations`, the joined schema); a nil schema gives Predict's schema-free plan; not an observed operation · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#559** Extension factory refusals are runtime-only: an extension regression's factory can refuse a request that predict accepts, because there is no predict-time hook. Found in U60 (E4-S1) · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#577** An extension aggregator whose `FieldInputs` reads a window label does not turn windows-first on, so it is refused as an unknown field on predict and the runtime alike (documented); closing it needs an `ExtensionRegistry` hook at every classifier site. Deferred from U63 · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#244** A general `target` / `predictors` refusal on every non-regression attribute (only `ATTR_CODE_IN` refuses them today) · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#245** Optional: move the `ATTR_CODE_IN` parse and accept rules into a neutral shared package instead of the predict copy pinned by `TestCodeIn_PredictRefusalsMatchRuntime` · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#486** Extension attributes declaring an output dictionary, so an `extend` attribute emitting a categorical inherits a dictionary the way `ATTR_PREVIOUS_STATE` inherits its state field's. Found in U55 (E1-S2) · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#676** Extension `OverlayKinds` registration: `internal/descriptor/extensions.go:103` reserves the `Options.Extensions.OverlayKinds` slot as "not yet implemented"; implement embedder-registered overlay kinds through predict, manifest, MCP and runtime, or remove the reserved slot · [U67](units/U67-predict-extension-parity-2.md)
- [ ] **#479** `WIN_FORMULA`: a general post-window formula site — an expression over a grouped row's columns and window outputs (ratios, differences, a lift of two shares) — so a gains-table figure beyond `WIN_SHARE` / `WIN_LIFT` needs no new operator. Ships in v1.0.0 (owner decision 2026-11-02; U49 shipped a dedicated `WIN_LIFT` and the general site is additive) · [U67](units/U67-predict-extension-parity-2.md)
- [x] **#708** Predict does not fold a loaded or inline model's `coef()` / `sweep_axis()` / `response()` values although they are in the file; they stay under `unresolved_scalars` and their range checks run at runtime. A design option (fold what a loaded file states), found in library-gaps E3-S2. Part 1 done (E5-S1): a window param or `AGG_CONSTANT` expr reading a request's own loaded `models` / `populations` entry is folded to the runtime's value and range-checked at predict (`scalarslot.Env.Loaded`, `model.Folding`; Compose `fits` stay unresolved); part 2 done (E5-S2): expression-capable extension params (Process, Compose slots, facet filterers and their `Validate` view) and Compose optimize bounds / starts reading a block's own `models` (`optblock.Env.Fold`, predict only) fold the same way · [U67](units/U67-predict-extension-parity-2.md)
