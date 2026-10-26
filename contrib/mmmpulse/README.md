# mmmpulse

`mmmpulse` was Pulse's media-mix modelling pack. Both of its models are now built-in regressions: `REG_MMM_HILL`, which fits every channel's effect and saturation curve jointly over already-adstocked channels, and `REG_MMM_BAYES`, which adstocks raw spend inside the model and samples the posterior with NUTS (the public `mcmc` package). Every instance, the stock `pulse` CLI and `pulse mcp` included, runs them with no registration, and their skills (`op-reg-mmm-hill`, `op-reg-mmm-bayes` and the `mmm-workflow` design guide) ship with Pulse. The module (`github.com/frankbardon/pulse/contrib/mmmpulse`) remains only so existing imports compile: `Extensions()` returns an empty registration set, and registering either name yourself is refused as `PULSE_EXTENSION_NAME_COLLISION`.

## The model

```
y = β0 + Σ_c β_c · hill(a_c, k_c, s_c) + Σ_j γ_j · z_j        hill(x, k, s) = x^s / (x^s + k^s)
```

The channels `a_c` are already-adstocked spend columns (`WIN_DISTRIBUTED_LAG` upstream, its decay a sweep axis); the model does not adstock. The controls `z_j` enter linearly. All of β0, β, γ, k and s are fitted together by bounded Levenberg–Marquardt, serially and FMA-free, so the same rows give the same bits on every architecture.

```json
{"type": "REG_MMM_HILL", "name": "mmm", "target": "sales",
 "predictors": ["tv_ad", "search_ad", "price"],
 "params": {"channels": ["tv_ad", "search_ad"]}, "holdout": "week >= 131"}
```

## Params

| Param | Type | Default | Meaning |
|---|---|---|---|
| `channels` | array | required | Predictors entering through the Hill curve; every other predictor is a linear control. |
| `nonnegative` | boolean | `true` | Bound each channel β at 0 from below. An unsupported channel is exactly 0. |
| `prior` | object | none | Gaussian prior `{channel, mean, precision}` on one channel's β, for an experiment-measured effect. |

A shared slot the model did not declare (`penalty`, `selection`, `covariance`, `family`) is `PROCESSING_CONFIG`. `holdout`, `resample`, `conf_level`, `vcov`, `save` and `ATTR_REG_PREDICT` scoring work. A negative channel value or an unknown `prior` key is `PROCESSING_CONFIG`. No convergence within 1000 iterations is `PROCESSING_REGRESSION_NO_CONVERGE`, and the slot fails alone.

## Coefficient names

In this order: `(intercept)`, the channel betas (predictor order), the controls (predictor order), `k:<channel>` for each channel, `s:<channel>` for each channel. Read any with `coef("mmm", "tv_ad")`, `coef("mmm", "k:tv_ad")`, `coef("mmm", "s:tv_ad")`, with `lower`, `upper` and `se` as the third argument. `outputs.k` and `outputs.s` repeat the curve parameters keyed by channel. The curves are bounded: k within [0.02, 20] × the channel mean, s within [0.5, 5].

## Standard errors and p-values

Standard errors are Gauss–Newton, `σ²·(JᵀJ)⁻¹`, mapped to k and s by the delta method. The rule is per parameter: `null` for a parameter that sits on a bound, and for the k and s of a channel whose β is 0 (the data cannot inform a curve with no effect); every other parameter gets its error from the inverse over the free block. This differs from `REG_OLS` with bounds, where one binding bound nulls every standard error. Here a channel pinned at 0 does not blind the rest.

The model reports no p-value itself. Under `resample` (bootstrap or jackknife) the engine refits per replicate and writes replicate p-values from the Wald statistic of each coefficient.

## Decay intervals

The decay is not a parameter of the fit, so it has no standard error. Bound it two ways:

- **Search spread.** In the sweep, read the decays of the near-best ranks by holdout RMSE. A channel whose near-best slots span `[0.1, 0.5]` has a decay the data does not pin down.
- **Bootstrap.** At the chosen decays, `resample: bootstrap` on the fit gives intervals for the betas, k and s conditional on those decays.

An underfunded channel (spend never reaches its half-saturation) sits on the β/k^s ridge. Its k and s are weakly identified even when its ROI and lift are right: judge it by contribution, not by k.

## The two-call recipe

**Call 1: search the decays.** A compose `sweep` in `lhs` mode, one axis per channel decay with a `range` (TV-like `[0.3, 0.8]`, digital `[0, 0.4]`), `samples` in the thousands, `rank` on `regressions.mmm.holdout.rmse` ascending. Each slot is one `WIN_DISTRIBUTED_LAG` per channel (`"decay": {"$var": "tv"}`) and one `REG_MMM_HILL`. About 400 samples over four channels did not hold the pass bars; 2,000 did. Raise `Options.Limits.MaxComposeSlots` to the sample count. The winner is `ranking[0]`; its decays are in the slot label.

**Call 2: fit and optimize.** A sweep winner cannot yet be bound as a fit, so call 2 is a request template whose `$var` placeholders are filled from call 1's winner: each channel's decay, its steady-state multiplier (`Σ decay^l` over `max_lag`, the adstock a constant weekly spend of 1 settles to), the current spend, bounds and the total budget. Render it with `pulse.RenderTemplate`. It carries:

- `fits`: the same windows and `REG_MMM_HILL`, decays now fixed.
- `requests`: a contribution slot per channel, `coef("mmm","tv_ad") * hill(tv_ad, coef("mmm","k:tv_ad"), coef("mmm","s:tv_ad"))`.
- `optimize`: maximize the summed curves at `multiplier × spend` under `sum(spend) = budget`, each channel bounded (say 0.5 to 1.5 × current).

At the optimum every free channel's `marginal` (its marginal ROI) equals the budget `multiplier`; a channel on a bound sits above or below it. Trust the answer by `converged` and `starts_agreeing`. On the joint-truth test fixture (one market, 156 weeks, four channels) the flow met: decay error ≤ 0.10, ROI error ≤ 6.3%, free-channel marginal ROI equal to the multiplier within 8e-6, lift error ≤ 13.7%, 8 of 8 starts agreeing.

Skills carry the detail: `mmm-workflow` (the flow), `op-reg-mmm-hill` (the model), and the core `compose-sweep-sampling` and `compose-optimize`.

## REG_MMM_BAYES: the Bayesian route

`REG_MMM_BAYES` adstocks raw spend (geometric, `max_lag` 13, no normalisation), saturates it with a Hill curve and samples the posterior with NUTS (the public `mcmc` package). Decay is a posterior parameter, so every interval carries decay uncertainty, which the sweep route cannot give. It returns ROI, marginal ROI and contribution intervals per channel, per-coefficient summaries, sampler diagnostics and, with `holdout_from`, a held-out score.

```json
{"type": "REG_MMM_BAYES", "name": "bayes", "target": "sales",
 "predictors": ["tv_ad", "search_ad", "price"],
 "params": {"channels": ["tv_ad", "search_ad"], "time": "week",
            "holdout_from": 131, "rng_seed": 7}}
```

Rows are one market, one row per period, with `params.time` an integer field that is contiguous with step 1; a weekly date column is refused as `time_gap`, a panel as `duplicate_time`. `chains` (4), `warmup` (1000), `draws` (1000, per chain), `credible_level` (0.9) and `target_accept` (0.8) are the sampler knobs; the skill `op-reg-mmm-bayes` has the full params table, output keys and priors. `resample`, `save`, the engine `holdout` and scoring are refused; the model owns its holdout.

**Default priors.** The decay prior is Beta(1, 1), uniform on (0, 1). A Beta(1, 3) pulls toward fast decay and, on the ridge decay shares with the half-saturation and the effect, biases a long-memory channel's ROI; uniform lets the data place it, and a practitioner with a belief sets `priors.<channel>.decay`. Hill shape s is LogNormal(0.3, 0.5) (median 1.35, covering concave and S-shaped curves); the half-saturation k is TruncNormal(1, 1, 0.02, 10) on the adstock scaled by peak spend; ROI is LogNormal(0.2, 0.9); a `beta` prior is HalfNormal(1). Intercept, controls and noise are weakly informative on the max-scaled data.

**Lift tests.** An experiment becomes an informative `priors.<channel>.roi`. The prior is only as good as the match between the experiment and the model's ROI (window, geography, definition of incrementality); Meridian's documentation warns of the same mismatch. See `mmm-workflow`.

**Reading a fit.** `outputs.diagnostics` carries max R̂, minimum bulk and tail ESS, divergences and tree-depth hits. A fit that fails them still returns, with a `PROCESSING_REGRESSION_DIAGNOSTIC` warning whose `details.reason` is `rhat_high`, `ess_low`, `divergences` or `treedepth_saturated`; under `Strict` the slot fails with that code. A 2 x 100-draw run always warns `ess_low`. mROI is analytic with the fitted curve held fixed. k and s are individually unidentifiable (the same ridge as the Hill model): judge ROI and contribution.

**Calibration.** The 50-replicate coverage study (fresh data each, 90% intervals) covered the true ROI and contribution 92% of the time overall (target band 80-98%): tv 86%, search 92%, display 90%, social 100% with wide intervals (about 13.6% of the true ROI, against 2.3% for tv) because an always-on channel is the hardest to identify. Median ROI bias was at most 0.5%, with no divergences and max R̂ 1.0047. Detail and the re-run command: `internal/regmmm/testdata/coverage-study.md`.

**Cost.** About 32-39 s per default fit (4 chains x 2000 iterations) on a laptop-class arm64 core. Same request and seed give the same bytes on any worker count and architecture.

## Test

```
go test ./internal/regmmm/
```

The models' tests, pass bars (the Hill two-call flow over `mcpserve`, the Bayes default fit against its committed hash) and FMA gates live with them in `internal/regmmm`; the Bayes pass bar takes about 30-70 seconds.
