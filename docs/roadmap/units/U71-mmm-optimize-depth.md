---
id: U71
slug: mmm-optimize-depth
title: "Optimizer and transform follow-ups around fitted MMM models are closed: a post-fit slot, analytic constraint gradients, in-model adstock and lag/window widening"
track: Vector & matrix
size: L
status: done
depends_on: [U77, U78]
soft_depends_on: [U70]
blocks: [U32]
todo_items: [489, 553, 551, 571, 581, 572, 705]
branch: mmm-optimize-depth
---

# U71 — mmm-optimize-depth

**Outcome:** Optimizer and transform follow-ups around fitted MMM models are closed: a post-fit slot, analytic constraint gradients, in-model adstock and lag/window widening.

**Track:** Vector & matrix · **Size:** L · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first) · **Soft:** U70

## Summary

The optimization and model-building surface around MMM fits: the general post-fit extension slot, analytic-gradient nonlinear constraints, in-model adstock estimation, a temporal train/test split, categorical `WIN_LAG` / `WIN_LEAD`, a synth `lag()` partition argument and `Explain` coverage of `optimize` blocks. The optimizer response helper (#570), data-driven bounds (#550) and the sweep-winner window parameter (#576) shipped in [U79](U79-library-gaps.md).

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#489** *Narrowed by U60.* The post-fit extension slot of #385: an embedder-registered computation over fitted results. The two uses it was filed for are now covered without a slot: response-curve evaluation by `coef()` + `hill()` in a Compose request, and budget allocation under constraints by the Compose `optimize` block ([U60](units/U60-mmm-estimation.md)); the `contrib/mmmpulse` pack (since retired: both MMM models are built-ins as of `v1.0.0-alpha.29`) needed no side channel. What stays open is the general slot itself (a registered operator that reads a fit and writes a result). Delivered by U71 (E3): `Extensions.PostFits` + the Compose `post_fit` slot · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#553** Nonlinear `optimize` constraints with analytic gradients (central finite differences only today). Deferred from U60 · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#551** In-model adstock estimation (decay as a fitted parameter with an analytic SE), ordered / gap-aware rows for extensions, and sequence-aware scorers. Today the decay is searched, not fitted, and has no SE. Deferred from U60 · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#571** A temporal mode for `FEAT_TRAIN_TEST_SPLIT` (a split at a time boundary); the regression `holdout` predicate substitutes today. Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#581** Widen `WIN_LAG` / `WIN_LEAD` to categorical sources, so a lagged category can be grouped by (every built-in window refuses a categorical field today, `PULSE_FIELD_TYPE_UNSUPPORTED`). A contract change. Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#572** A synth `lag()` partition argument, so a lagged synth column restarts per group (no geo synth yet). Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [x] **#705** `Explain` does not describe Compose `optimize` blocks at all (no step, no finding for a block's bounds, `result()` or `response()` reads); pre-existing, found in library-gaps E7-S2 · [U71](units/U71-mmm-optimize-depth.md)

## Decisions kept

- Analytic gradients are the default for `optimize`; `gradient: "fd"` restores the old solves. A block calling a function without a derivative rule falls back WHOLE to finite differences, with a predict advisory.
- The `gradient` / `gradient_fallback` echo is ABSENT on a failed `{name, direction, error}` optimize entry (the slot-error contract); owner 2026-11-15: keep as is. Not a deferral.
- Carry-over is the default for an in-model adstocked fit, so `REG_MMM_BAYES` holdout figures move; `carryover: false` restores them.
- Leverage / influence over a `REG_MMM_HILL` fit read its tangent plane (#732, pulled back in-effort as E2-S6, owner 2026-11-16); over `REG_MMM_BAYES` they stay a coded `type_unsupported` refusal by design (a posterior has no hat matrix; PSIS-LOO Pareto k would be a new operator). Not a deferral.
- A post-fit operator is embedder-only: a recipe served through `Extensions.Examples`, not a shipped example.

## Handed-on

| Item | To | Why |
|---|---|---|
| #733 `make sites` misses regression-slot sites (`regadapt`, `levelFields`, `slotfail`, schema enum, CLI strict test, example profile) | [U32](U32-docs-audit.md) | same tooling gap family as #706 / #715 |
