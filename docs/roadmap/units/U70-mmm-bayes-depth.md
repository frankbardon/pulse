---
id: U70
slug: mmm-bayes-depth
title: "Bayesian marketing-mix models gain hierarchy, richer kernels and curves, lift-test data, posterior-aware optimization, raw draws, scoring and alternative samplers"
track: Vector & matrix
size: L
status: not-started
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [552, 561, 562, 563, 564, 565, 566, 567, 568, 578]
branch: mmm-bayes-depth
---

# U70 — mmm-bayes-depth

**Outcome:** Bayesian marketing-mix models gain hierarchy, richer kernels and curves, lift-test data, posterior-aware optimization, raw draws, scoring and alternative samplers.

**Track:** Vector & matrix · **Size:** L · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first)

## Summary

Bayesian MMM depth: geo-hierarchical partial pooling and a delayed or Weibull adstock kernel, non-Hill saturation, a lift-test likelihood, a posterior-aware budget optimizer, raw posterior draws on the wire, save and scoring for `REG_MMM_BAYES`, alternative samplers behind `mcmc.Sampler`, and the doctored-sampler health-warning test pinned to a seed.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#552** *Narrowed by U62.* Partial pooling / geo-hierarchical MMM as a pack on the `REG_MMM_HILL` contract. The Bayesian part is done: `REG_MMM_BAYES` ([U62](units/U62-bayesian-mmm.md); shipped in `contrib/mmmpulse`, a built-in regression since `v1.0.0-alpha.29`) samples a single-market posterior with NUTS. What stays open is the geo hierarchy (partial pooling across markets). Deferred from U60 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#561** Geo-hierarchical MMM: partial pooling of channel effects, decay and curves across markets (a panel input is refused by `REG_MMM_BAYES` today as `duplicate_time`). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#562** Weibull and delayed adstock kernels for `REG_MMM_BAYES` (`params.adstock` accepts `geometric` only; `WIN_DISTRIBUTED_LAG` already has the Weibull weights). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#563** Saturation curves other than Hill for `REG_MMM_BAYES` (for example logistic or Michaelis-Menten forms). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#564** A lift-test likelihood: a measured experiment enters as data, not only as an informative `priors.<channel>.roi` (the estimand-mismatch caveat in `ext-mmm-workflow` applies to the prior route). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#565** A posterior-aware budget optimizer: Compose `optimize` over `REG_MMM_BAYES` draws, so an allocation carries the posterior uncertainty; today the Bayesian route has no optimizer and the point-estimate `optimize` reads `coef()` means only. Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#566** Raw posterior draws on the wire (`REG_MMM_BAYES` returns summaries and diagnostics only). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#567** Save and scoring (`ATTR_REG_PREDICT`, engine `holdout`) for `REG_MMM_BAYES` with pre-adstocked inputs, so a Bayesian fit can score new rows without a row-order dependency. Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#568** Samplers other than NUTS behind `mcmc.Sampler` / `params.sampler` (for example HMC with a fixed path length, or variational inference). Deferred from U62 · [U70](units/U70-mmm-bayes-depth.md)
- [ ] **#578** `TestMMMBayes_HealthWarnings` doctored-sampler subtests pin `rng_seed` 8392533999830091673 (the old derived seed), because the new canonical seed flagged `rhat_high` on the intercept baseline: make the test independent of a particular draw. Deferred from U63 · [U70](units/U70-mmm-bayes-depth.md)
