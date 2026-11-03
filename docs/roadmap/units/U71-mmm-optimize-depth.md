---
id: U71
slug: mmm-optimize-depth
title: "Optimizer and transform follow-ups around fitted MMM models are closed: a post-fit slot, analytic constraint gradients, in-model adstock and lag/window widening"
track: Vector & matrix
size: L
status: not-started
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

- [ ] **#489** *Narrowed by U60.* The post-fit extension slot of #385: an embedder-registered computation over fitted results. The two uses it was filed for are now covered without a slot: response-curve evaluation by `coef()` + `hill()` in a Compose request, and budget allocation under constraints by the Compose `optimize` block ([U60](units/U60-mmm-estimation.md)); the `contrib/mmmpulse` pack (since retired: both MMM models are built-ins as of `v1.0.0-alpha.29`) needed no side channel. What stays open is the general slot itself (a registered operator that reads a fit and writes a result). Additive; ships in v1.x · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#553** Nonlinear `optimize` constraints with analytic gradients (central finite differences only today). Deferred from U60 · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#551** In-model adstock estimation (decay as a fitted parameter with an analytic SE), ordered / gap-aware rows for extensions, and sequence-aware scorers. Today the decay is searched, not fitted, and has no SE. Deferred from U60 · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#571** A temporal mode for `FEAT_TRAIN_TEST_SPLIT` (a split at a time boundary); the regression `holdout` predicate substitutes today. Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#581** Widen `WIN_LAG` / `WIN_LEAD` to categorical sources, so a lagged category can be grouped by (every built-in window refuses a categorical field today, `PULSE_FIELD_TYPE_UNSUPPORTED`). A contract change. Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#572** A synth `lag()` partition argument, so a lagged synth column restarts per group (no geo synth yet). Deferred from U63 · [U71](units/U71-mmm-optimize-depth.md)
- [ ] **#705** `Explain` does not describe Compose `optimize` blocks at all (no step, no finding for a block's bounds, `result()` or `response()` reads); pre-existing, found in library-gaps E7-S2 · [U71](units/U71-mmm-optimize-depth.md)
