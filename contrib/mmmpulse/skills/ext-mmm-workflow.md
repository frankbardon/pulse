---
name: ext-mmm-workflow
description: Media-mix workflow with the media-mix pack's Hill regression — search adstock decays with an lhs sweep, fit the Hill model at the winner, then reallocate budget with compose optimize. The two-call recipe, decay intervals, what to trust.
kind: design
type: guide
applies_to: compose
requires: [REG_MMM_HILL, WIN_DISTRIBUTED_LAG, capability:compose_sweep_sampling, capability:compose_optimize]
covers: [media mix, MMM, adstock decay, Hill curve, marginal ROI, budget reallocation, two-call recipe]
---

# Media-mix workflow

Goal: per-channel decay, response curve and a budget split that equalises marginal ROI. The decay is nonlinear in the adstock, so it is searched; the curves are fitted jointly by `REG_MMM_HILL`.

## Call 1: search the decays

An `lhs` sweep (`compose-sweep-sampling`) with one axis per channel decay, a `range` each (TV-like `[0.3, 0.8]`, digital `[0, 0.4]`), `samples` in the thousands, `rank` `{by: "regressions.mmm.holdout.rmse", order: "asc"}`. Each slot: one `WIN_DISTRIBUTED_LAG` per channel (`decay: {"$var": "tv"}`), then one `REG_MMM_HILL` with `params.channels` and a `holdout` on the last weeks. ~400 samples over four channels was not enough; raise `Options.Limits.MaxComposeSlots` to fit the count. The winner is `ranking[0]`, its decays read off the slot label.

## Call 2: fit and optimize

A winning sweep slot cannot yet be bound as a fit, so template it. Keep call 2 as a request template whose `$var`s are the winner's decays, each channel's steady-state multiplier (the geometric weights summed over `max_lag`, `Σ decay^l` for `normalize: false`: a constant weekly spend settles to that multiple), current spend, bounds and the budget; fill it from call 1 (`pulse.RenderTemplate`). It holds `fits` (the same windows and regression, decays now fixed), a `requests` entry for contributions (`coef(...) * hill(x, coef(.. "k:ch"), coef(.. "s:ch"))`), and an `optimize` block maximising the summed curves at `mult * spend` under `sum(spend) = budget` (`compose-optimize`). At the optimum every free channel's `marginal` equals the budget `multiplier`.

## Intervals and trust

- Decay: no SE exists. Report the range of decays among the sweep's near-best ranks, and bootstrap call 2's fit (`resample`) for the betas.
- Curves: an underfunded channel's k and s are weakly identified; trust its ROI, not its k.
- Check `starts_agreeing` on the optimize result.
- On the joint-truth fixture the pack met: decay worst error 0.10, ROI worst 6%, free-channel marginal ROI equal to the multiplier within 1e-5, lift worst 14%, 8/8 starts agreeing.

## See

`op-reg-mmm-hill` · `compose-sweep-sampling` · `compose-optimize` · `compose-fits` · `regression-model-options`
