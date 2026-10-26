```yaml
name: mmm-workflow
description: Media-mix workflow with the built-in media-mix regressions — search adstock decays with an lhs sweep, bind the winner as the Hill fit, score contributions and reallocate budget with compose optimize, all in one compose call; or fit the Bayesian model for intervals. The one-call recipe, decay intervals, lift-test priors, what to trust.
kind: design
type: guide
applies_to: compose
requires: [REG_MMM_HILL, WIN_DISTRIBUTED_LAG, WIN_LAG, ATTR_FORMULA, capability:for_each, capability:compose_sweep_sampling, capability:compose_sweep_bind, capability:compose_optimize]
covers: [Bayesian media mix, lift test prior, media mix, MMM, adstock decay, Hill curve, marginal ROI, budget reallocation, one-call recipe, sweep_axis]
```

# Media-mix workflow

Goal: per-channel decay, response curve and a budget split that equalises marginal ROI. The decay is nonlinear in the adstock, so it is searched; the curves are fitted jointly by `REG_MMM_HILL`. One compose call does it all: no template, no second call, no label parsing.

## The one call

- **`sweep`**: `lhs` ([`compose-sweep-sampling`](compose-sweep-sampling.md)), one axis per channel decay with a `range` (TV-like `[0.3, 0.8]`, digital `[0, 0.4]`; keep `max` below 1), `samples` in the thousands, `rank` `{by: "regressions.mmm.holdout.rmse", order: "asc"}`. Each slot: one `WIN_DISTRIBUTED_LAG` per channel (`kernel: geometric`, `decay: {"$var": "tv"}`, `normalize: false`), then one `REG_MMM_HILL` named `mmm` with `params.channels` and a `holdout` on the last weeks. ~400 samples over four channels was not enough; 2,000 were. `MaxComposeSlots` counts the samples plus the explicit slots: raise `Options.Limits.MaxComposeSlots` (CLI `--limit max_compose_slots=2001` for 2,000 samples and one explicit slot).
- **`fits`**: `[{"name": "mmm", "from_sweep": {"regression": "mmm"}}]` refits the winning slot's regression, its holdout kept ([`compose-sweep-bind`](compose-sweep-bind.md)).
- **`requests`**: one contribution slot. A window's `decay` is a literal, so rebuild each adstock at the winner's decay: one `WIN_LAG` item (ordered by week) fanned out with `for_each` over channel × lag `1..max_lag`, label `{{ch}}_l{{lag}}` (`params: {offset: {"$var": "lag"}, default: 0}`, [`item-fan-out`](item-fan-out.md)), then an `ATTR_FORMULA` per channel labelled `tv_ad`, `tv + d * (tv_l1 + d * (tv_l2 + … + d * tv_l13))` with `d` written as `sweep_axis("mmm", "tv")`, then `coef("mmm", "tv_ad") * hill(tv_ad, coef("mmm", "k:tv_ad"), coef("mmm", "s:tv_ad"))`.
- **`optimize`**: maximise the summed curves at steady state, `coef("mmm", "tv_ad") * hill(m * tv, …)` with the multiplier `m = (1 - d ^ L) / (1 - d)`, `L = max_lag + 1`, `d = sweep_axis("mmm", "tv")` (the adstock a constant weekly spend of 1 settles to), under `sum(spend) = budget` ([`compose-optimize`](compose-optimize.md)). Current spend, bounds and the budget are your own figures in `params`.

Run order: the sweep slots, the rank, the bound fit, then optimize and the contribution slot. The winning decays are `ranking[0].axes`, typed numbers. At the optimum every free channel's `marginal` equals the budget `multiplier`.

## Bayesian route vs LM/sweep route

- **LM/sweep** (above): fast (seconds per fit), scores holdout, saves and feeds `compose optimize`. Decay is a sweep axis, so betas, k and s are decay-conditional; there is no decay interval.
- **`REG_MMM_BAYES`** ([`op-reg-mmm-bayes`](op-reg-mmm-bayes.md)): one fit adstocks raw spend and returns ROI, mROI and contribution intervals that include decay uncertainty; about 35 s per default fit. It has no save, scoring or optimizer: take its ROI intervals to a budget decision by hand. Needs `params.time` as contiguous integers (a date column is refused as `time_gap`).
- Pick the sweep to allocate budget, Bayes to say how sure you are. Reading an interval: calibration over 50 replicates was 92% at the 90% level (tv 86%, social 100% with wide intervals), so read social's width, not its centre. Check `diagnostics` first; `Strict` turns a sampler warning into a failed slot.

## Lift test as a prior

Turn an experiment into `priors.<channel>.roi`: a lognormal with `mu = ln(measured ROI)` and `sigma` from the experiment's interval (about `ln(upper/lower)/3.3` for a 90% interval). Widen it, never narrow it. As Meridian's own guidance warns, the prior is only as good as the match between estimands: the experiment's window, geography and definition of incrementality must be the model's ROI (outcome over the fitted period against zero spend). A short geo test is not that, so inflate `sigma` for the mismatch. This is a prior, not a likelihood: the data can overrule it.

## Intervals and trust

- Decay: no SE exists. Report the range of decays among the sweep's near-best ranks (`ranking[i].axes`). For beta intervals, `resample` an authored fit at the winning decays: on the sweep regression it would run in every slot.
- Curves: an underfunded channel's k and s are weakly identified; trust its ROI, not its k.
- Check `starts_agreeing` on the optimize result.
- Measured once on a synthetic four-channel cohort with known truth (2,000 samples, 13 sweep seeds): decay worst error 0.10, ROI worst 9%, lift worst 12%, free-channel marginal ROI equal to the multiplier within 1e-5, 8/8 starts agreeing.

## See

[`op-reg-mmm-hill`](op-reg-mmm-hill.md) · [`op-reg-mmm-bayes`](op-reg-mmm-bayes.md) · [`compose-sweep-sampling`](compose-sweep-sampling.md) · [`compose-sweep-bind`](compose-sweep-bind.md) · [`compose-optimize`](compose-optimize.md) · [`item-fan-out`](item-fan-out.md) · [`regression-model-options`](regression-model-options.md)
