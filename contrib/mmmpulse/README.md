# mmmpulse

`mmmpulse` is Pulse's media-mix modelling pack. It registers two regressions: `REG_MMM_HILL`, which fits every channel's effect and saturation curve jointly over already-adstocked channels, and `REG_MMM_BAYES`, which adstocks raw spend inside the model and samples the posterior with NUTS (the public `mcmc` package). The pack lives in its own Go module (`github.com/frankbardon/pulse/contrib/mmmpulse`), versioned in lockstep with Pulse, and imports only Pulse's public packages.

## Register it

```go
import (
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/mmmpulse"
)

p, err := pulse.New(pulse.Options{Extensions: mmmpulse.Extensions()})
```

`Extensions()` returns the regressions and the pack's skills (`op-reg-mmm-hill`, `ext-mmm-workflow`), served by `p.Skills()` and `pulse_skills_list` like the built-in pack. To add your own extensions, append to `Regressions` and keep one `Skills` filesystem that holds both skill sets. The stock `pulse` CLI and `pulse mcp` do not register packs; an embedder, or `mcpserve` with `Options.Extensions`, does. Unregistered, `REG_MMM_HILL` is `PULSE_REGRESSION_INVALID`.

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

At the optimum every free channel's `marginal` (its marginal ROI) equals the budget `multiplier`; a channel on a bound sits above or below it. Trust the answer by `converged` and `starts_agreeing`. On the pack's joint-truth test fixture (one market, 156 weeks, four channels) the flow met: decay error ≤ 0.10, ROI error ≤ 6.3%, free-channel marginal ROI equal to the multiplier within 8e-6, lift error ≤ 13.7%, 8 of 8 starts agreeing.

Skills carry the detail: `ext-mmm-workflow` (the flow), `op-reg-mmm-hill` (the model), and the core `compose-sweep-sampling` and `compose-optimize`.

## Test

```
cd contrib/mmmpulse && go test ./...
```

The pass-bar test drives the whole flow over `mcpserve` and takes about 20 seconds.
