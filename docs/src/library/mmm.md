# Media-Mix Models

Pulse ships two built-in media-mix regressions: `REG_MMM_HILL`, which fits every channel's effect and saturation curve jointly over already-adstocked channels, and `REG_MMM_BAYES`, which adstocks raw spend inside the model and samples the posterior with NUTS (the public `mcmc` package). Every instance, the stock `pulse` CLI and `pulse mcp` included, runs them with no registration, and their skills (`op-reg-mmm-hill`, `op-reg-mmm-bayes` and the `mmm-workflow` design guide) ship with Pulse. An embedder registering either name is refused as `PULSE_EXTENSION_NAME_COLLISION`.

Before `v1.0.0-alpha.29` both models shipped in a separate contrib pack module registered through `pulse.Options.Extensions`; that module is removed, so drop its import and its `Extensions` entry (see the embedder migration guide).

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

## The one-call recipe

One `ComposedRequest` searches the decays, fits the curves at the winner, scores each channel's contribution and reallocates the budget. There is no template, no second call and no label parsing.

```jsonc
{
  "params": {"budget": 256.4, "mean_tv": 102.9, "lo_tv": 51.4, "hi_tv": 154.3, "...": "the same per channel"},
  "sweep": {
    "mode": "lhs", "samples": 2000,
    "axes": [{"name": "tv",     "range": {"min": 0.3, "max": 0.8}},
             {"name": "search", "range": {"min": 0.0, "max": 0.4}}],
    "rank": {"by": "regressions.mmm.holdout.rmse", "order": "asc"},
    "request": {
      "cohort": {"filename": "weekly.pulse"},
      "windows": [{"type": "WIN_DISTRIBUTED_LAG", "field": "tv", "label": "tv_ad",
                   "order_by": [{"field": "week"}],
                   "params": {"kernel": "geometric", "decay": {"$var": "tv"}, "max_lag": 13, "normalize": false}},
                  {"...": "one window per channel"}],
      "regressions": [{"type": "REG_MMM_HILL", "name": "mmm", "target": "sales",
                       "predictors": ["tv_ad", "search_ad", "price"],
                       "params": {"channels": ["tv_ad", "search_ad"]}, "holdout": "week >= 131"}],
      "return": {"preset": "standard", "exclude": ["data"]}}
  },
  "fits": [{"name": "mmm", "from_sweep": {"regression": "mmm"}}],
  "requests": [{
    "label": "contributions",
    "cohort": {"filename": "weekly.pulse"},
    "windows": [{"for_each": {"axes": [{"name": "ch",  "values": ["tv", "search"]},
                                       {"name": "lag", "values": [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13]}]},
                 "type": "WIN_LAG", "field": "{{ch}}", "label": "{{ch}}_l{{lag}}",
                 "order_by": [{"field": "week"}], "params": {"offset": {"$var": "lag"}, "default": 0}}],
    "attributes": [
      {"type": "ATTR_FORMULA", "label": "tv_ad",
       "expression": "tv + sweep_axis(\"mmm\", \"tv\") * (tv_l1 + sweep_axis(\"mmm\", \"tv\") * (tv_l2 + sweep_axis(\"mmm\", \"tv\") * (tv_l3 + sweep_axis(\"mmm\", \"tv\") * (tv_l4 + sweep_axis(\"mmm\", \"tv\") * (tv_l5 + sweep_axis(\"mmm\", \"tv\") * (tv_l6 + sweep_axis(\"mmm\", \"tv\") * (tv_l7 + sweep_axis(\"mmm\", \"tv\") * (tv_l8 + sweep_axis(\"mmm\", \"tv\") * (tv_l9 + sweep_axis(\"mmm\", \"tv\") * (tv_l10 + sweep_axis(\"mmm\", \"tv\") * (tv_l11 + sweep_axis(\"mmm\", \"tv\") * (tv_l12 + sweep_axis(\"mmm\", \"tv\") * (tv_l13)))))))))))))"},
      {"type": "ATTR_FORMULA", "label": "tv_contrib",
       "expression": "coef(\"mmm\", \"tv_ad\") * hill(tv_ad, coef(\"mmm\", \"k:tv_ad\"), coef(\"mmm\", \"s:tv_ad\"))"},
      {"...": "the same per channel"}]
  }],
  "optimize": [{
    "name": "allocation", "direction": "maximize", "starts": 8,
    "variables": [{"name": "tv", "lower": "params.lo_tv", "upper": "params.hi_tv", "start": "params.mean_tv"},
                  {"...": "one per channel"}],
    "objective": "response(\"mmm\", \"tv_ad\", tv) + ...",
    "constraints": [{"expr": "tv + search - params.budget", "type": "eq"}]
  }]
}
```

- **The sweep** is an `lhs` search with one decay axis per channel, each a `range` (TV-like `[0.3, 0.8]`, digital `[0, 0.4]`; keep the maximum below 1). Each slot is one `WIN_DISTRIBUTED_LAG` per channel and one `REG_MMM_HILL` with a `holdout`, ranked by holdout RMSE. Use enough samples for the number of channels: the space grows with every axis, so check that the best slots agree on the decays before trusting one winner. The winning decays are `ranking[0].axes`, as typed numbers.
- **The bound fit** `fits[].from_sweep` refits the winning slot's regression, holdout kept, under the name `mmm`. Explicit slots and `optimize` read it with `coef("mmm", …)` (see the Compose sweeps page).
- **The contribution slot** cannot set a window's `decay` from the winner, because window params are literals. Instead it rebuilds each adstock: one `WIN_LAG` item fanned out with `for_each` over channel × lag `1..max_lag` (missing history is `0`), then a formula `x + d·(x_l1 + d·(x_l2 + …))` with `d = sweep_axis("mmm", "<channel>")`. That is the same sum `WIN_DISTRIBUTED_LAG` computes, so the result matches it to rounding. A contribution is `coef · hill(adstock, k, s)`.
- **The optimize block** maximises the summed curves at steady state, each `response("mmm", "<channel>", spend)` (below). It runs under `sum(spend) = budget`, each channel bounded (say 0.5 to 1.5 × current). Budget and shares are the caller's figures in `params`; current spend can come from the data instead: add an ungrouped slot `current` summing each channel's spend, and write the bounds `"result(\"current\", \"tv\") * params.lower_share"`. The slot runs once, before the block, and keeps its place in `responses`.
- **Slots.** `MaxComposeSlots` counts the sweep's samples plus the explicit slots, so 2,000 samples and the contribution slot need 2,001. Raise `Options.Limits.MaxComposeSlots`, or pass `pulse --limit max_compose_slots=2001 api compose --request mmm.json --json` on the CLI.

The batch runs the sweep slots first, then the rank, the bound fit, `optimize` and the contribution slot. Responses keep index order, so the contribution slot comes first. Serial and parallel runs give the same bytes. A pinned `rng_seed` (or the echoed `sweep_seed`) replays the draws.

At the optimum every free channel's `marginal` (its marginal ROI) equals the budget `multiplier`. A channel on a bound sits above or below it. Trust the answer by `converged` and `starts_agreeing`.

Skills carry the detail: `mmm-workflow` (the flow), `op-reg-mmm-hill` (the model), and the core `compose-sweep-bind`, `compose-sweep-sampling` and `compose-optimize`.

**Adstock lineage.** A fit records how each channel was adstocked, so a later reader never guesses a decay or `max_lag`. A `REG_MMM_HILL` channel that is the output of exactly one `WIN_DISTRIBUTED_LAG` records that window's resolved params (`kernel`, `decay`, `decay` + `peak` or `shape` + `scale`, `max_lag`, `normalize`). A decay written as `sweep_axis()` or `params.d` is recorded as the number the window read. A `REG_MMM_BAYES` channel records the fit's `params.adstock` kernel at the posterior means of its coefficients (`decay:<channel>`; `decay:` and `peak:<channel>`; `shape:` and `scale:<channel>`) over the fit's `max_lag`, unnormalised. A channel built any other way (`WIN_LAG` items and a formula, a feature) gets no record. The record lives on the bound fit, and a `save` writes it as the model file's `adstock` feature.

**Steady-state response.** `response(fit, channel, spend)` is the channel's contribution once a constant spend has run long enough to fill the adstock window: `beta · hill(m · spend, k, s)`. `beta`, `k` and `s` are the fit's `<channel>`, `k:<channel>` and `s:<channel>` coefficients (a `REG_MMM_BAYES` fit's posterior means). A `REG_MMM_BAYES` fit with `params.saturation` `logistic` or `michaelis_menten` is read on its own curve instead, over `lam:<channel>` or `k:<channel>` (the fit's curve record; `hill()` itself stays the Hill formula). `m` is the sum of the channel's adstock kernel weights over lags `0..max_lag`, the level a constant spend of 1 settles to: `(1 − d^(max_lag+1)) / (1 − d)` for an unnormalised geometric kernel, 1 for any normalised kernel, the plain sum for a Weibull one. On a `REG_MMM_BAYES` geo panel it reads the population's terms; a fourth argument, `response(fit, channel, spend, geo)`, reads that geo's own (Geo panels, below). It reads the fit's adstock record and never a default, so it replaces the hand-written `coef · hill(m · x, k, s)` algebra and its copied decay. A channel with no record is `PULSE_MODEL_TERM_UNKNOWN` with `details.reason: "adstock_unknown"`, `details.channel` and the channels that have one under `details.channels`. Predict raises it for a call whose fit and channel are literals, except on a `from_sweep` fit: its record comes from the winning slot, so the run judges it. It works on in-request, saved, loaded and inline models, in `optimize` objectives, constraints and bounds and in formulas and filters. `response` is reserved: an embedder function of that name is `PULSE_EXTENSION_NAME_RESERVED`.

## An inline truth model

A synthetic cohort comes with the curve that generated it. Declare that truth as an inline `models` entry, then run the same `optimize` objective over the fit in one block and over the truth in another. No file is needed, and the fit's allocation can be compared with the true optimum:

```json
"models": [{"name": "truth", "inline": {
  "model_format_version": 1, "type": "REG_MMM_HILL", "target": "sales",
  "predictors": ["tv_ad", "price"], "terms": ["tv_ad", "price"],
  "coefficients": {"(intercept)": 100, "tv_ad": "params.beta_tv",
                   "k:tv_ad": "params.k_tv", "s:tv_ad": 1.5, "price": -2}}}]
```

- **The body.** It is the model-file document (`model_format_version` 1 plus `features`, as a `save` writes). A number in it may be a scalar expression over literals and `params.<name>`. It cannot call `coef()`, `sweep_axis()`, `response()`, `lookup()` or an embedder function, so one model never reads another and predict knows every value. `+Inf` / `-Inf` stay literals.
- **Types and terms.** The type is `REG_OLS`, `REG_GLM`, `REG_BAYES_LINEAR`, `REG_MMM_HILL` or `REG_MMM_BAYES`. The coefficients name exactly the terms the type reports. That is `(intercept)` and every predictor, plus, for a media-mix model, `k:` / `s:` (Bayes: `decay:` too, and `sigma`) for every channel. A channel is any predictor with a curve coefficient. A stray name is `PULSE_MODEL_REF_INVALID` `inline_term_unknown`, an absent one `inline_term_missing`, another type `inline_type_unsupported`.
- **What reads it.** `coef()` reads it, and so does `sweep_axis()` when the body carries `sweep_axes` (feature `sweep_axes`): `"features": ["sweep_axes"], "sweep_axes": {"tv": 0.6}`. It takes the one model namespace, so a name a fit or another entry binds is `PULSE_MODEL_NAME_COLLISION`. Nothing scores it. `ATTR_REG_PREDICT` or a `params.fit` attribute naming it is `PULSE_MODEL_SCORE_INVALID` `inline_not_scorable`.
- **Adstock.** The body may declare its channels' adstock as a model file does: `"features": ["adstock"], "adstock": {"tv_ad": {"kernel": "geometric", "decay": "params.d_tv", "max_lag": 8, "normalize": true}}`. Its `decay`, `peak`, `shape`, `scale` and `max_lag` are inline numbers too. An inline `REG_MMM_BAYES` model's terms follow the kernel its coefficients name: a `peak:<channel>` means delayed, a `shape:` or `scale:<channel>` a Weibull kernel. Each key must be a channel, and each record a kernel `WIN_DISTRIBUTED_LAG` accepts. `response()` reads it, so one objective serves the fit and the truth.
- **Exactly one** of `path` / `inline` per entry (`path_and_inline`). A bad number is `inline_invalid` / `inline_not_numeric` / `inline_not_finite` / `inline_not_integer` (`n_obs`), with its key path under `details.field`. A params refusal keeps its own code. Predict refuses every one of these with the runtime's code, message and details.

## Contribution by quarter

A per-week contribution column is rarely the deliverable; a per-quarter table is. Add `groups` and one `AGG_SUM` per channel to the contribution slot of the recipe above, keeping its windows and attributes as they are:

```jsonc
{
  "label": "contribution_by_quarter",
  "cohort": {"filename": "weekly.pulse"},
  "windows": [{"...": "the WIN_LAG for_each item of the contribution slot"}],
  "attributes": [{"...": "the tv_ad adstock formula, per channel"},
                 {"type": "ATTR_FORMULA", "label": "tv_contrib",
                  "expression": "coef(\"mmm\", \"tv_ad\") * hill(tv_ad, coef(\"mmm\", \"k:tv_ad\"), coef(\"mmm\", \"s:tv_ad\"))"},
                 {"...": "the same per channel"}],
  "groups": [{"type": "GROUP_DATE", "field": "week_start", "params": {"component": "quarter"}}],
  "aggregations": [{"type": "AGG_SUM", "field": "tv_contrib", "label": "tv_contribution"},
                   {"type": "AGG_SUM", "field": "sales", "label": "sales"}],
  "sort": [{"field": "week_start"}]
}
```

`week_start` stands for any `date` (or `datetime`) column on the weekly rows. An aggregation that reads `tv_contrib`, a label computed from the window columns, makes the slot run windows-first. The lags, the adstock and the contribution are computed per week, in week order, over every row of the market. The rows are then grouped by quarter and summed. The response has one row per quarter (`week_start` is the quarter key, such as `2024-Q1`), with each channel's contribution beside total sales. The same request over a cohort that stored `tv_contrib` as a column gives the same figures. `pulse api predict-compose` shows the plan in the slot's `post_window` block (`windows_first: true`).

- **Weights.** A request-level `weight` is refused here (`PROCESSING_CONFIG`: a window has no weighted form). To weight the sums, put a `weight` on each aggregation.
- **Per market.** On a panel, add the market field to each window's `partition_by` so the lags restart per market, and add a second grouper on the market for a market × quarter table.
- **Sort.** Sort by the group key or an aggregation label. A window or attribute label is not in the output rows, so sorting by one is refused.

The skill `windows-first` covers the general rule, also for ungrouped and crosstab requests.

## REG_MMM_BAYES: the Bayesian route

`REG_MMM_BAYES` adstocks raw spend (geometric, `max_lag` 13, no normalisation, by default), saturates it (a Hill curve by default) and samples the posterior with NUTS (the public `mcmc` package). The adstock's parameters are posterior parameters, so every interval carries carry-over uncertainty, which the sweep route cannot give. It returns ROI, marginal ROI and contribution intervals per channel, per-coefficient summaries, sampler diagnostics and, with `holdout_from`, a held-out score.

```json
{"type": "REG_MMM_BAYES", "name": "bayes", "target": "sales",
 "predictors": ["tv_ad", "search_ad", "price"],
 "params": {"channels": ["tv_ad", "search_ad"], "time": "week",
            "holdout_from": 131, "rng_seed": 7}}
```

Rows are one market, one row per period, with `params.time` an integer field that is contiguous with step 1; a weekly date column is refused as `time_gap`, and a panel without `params.geo` as `duplicate_time`. `chains` (4), `warmup` (1000), `draws` (1000, per chain), `credible_level` (0.9) and `target_accept` (0.8; 0.95 on a geo panel) are the sampler knobs; the skill `op-reg-mmm-bayes` has the full params table, output keys and priors. `resample` is refused. `save`, the engine `holdout` and scoring need `return_draws: true` (else `draws_required`): the scorer averages α + Σγ·z + Σβ·curve(x) over the stored draws, on pre-adstocked channel columns built with `WIN_DISTRIBUTED_LAG` from the saved `adstock.<channel>` record (`fixed_at: "posterior_mean"`); `bound: lower|upper` on `ATTR_REG_PREDICT` gives the credible bounds of that mean prediction. On a geo panel the scoring frame needs the geo field, and an unseen geo scores from the population with `PULSE_MMM_GEO_UNSEEN`. `holdout_from` stays the history-aware holdout; the engine `holdout` scores the held-out rows' columns as given, with no carry-over (skill `mmm-bayes-posterior`).

**Geo panels.** `params.geo` names the market field (a categorical, or an unsigned integer code) and fits one hierarchical model over a long geo x period panel. Each geo is its own series: its `params.time` is contiguous between its first and last period (ragged starts and ends are fine; an inner gap is refused as `geo_time_gap`, a repeat within a geo as `duplicate_time`). `Options.Limits.MaxMMMGeos` caps the geos (default 50, `pulse.Unlimited` to lift it; over it is `PULSE_LIMIT_EXCEEDED`, and predict reports a `possible` finding from a categorical's dictionary). A channel that never spends in some geo is refused (its effect there is unidentified).

The model partially pools each channel's effect (its ROI, or β under a `beta` prior) and every parameter of the adstock kernel across the geos, non-centred: on the prior's unconstrained scale (log ROI, logit decay, …) a geo's value is `μ + τ·η`, with `η ~ Normal(0, 1)` and the scale `τ ~ HalfNormal(0.5)`. The channel's prior (`priors.<channel>`) is the prior of the population value μ. Controls and σ are shared; each geo gets its own intercept. Curves are shared too unless `params.per_geo_curves: true`, which pools each curve parameter across the geos the same way (on its log scale), so every geo has its own curve. The flag needs `params.geo` (on one market it is refused with reason `per_geo_curves_without_geo`, by predict too); absent or `false`, the fit is the shared-curve fit bit for bit. All geos share one scaling, so a figure means the same in every geo.

- **Names.** A pooled term keeps its name as the population value (`tv`, `decay:tv`); geo NE's is `tv[NE]` / `decay:tv[NE]` (and `(intercept)[NE]`), the spread `tau:tv` / `tau:decay:tv`. With per-geo curves the curve terms follow suit (`k:tv`, `k:tv[NE]`, `tau:k:tv`). `coef(fit, "tv[NE]")` and `coef(fit, "tau:tv")` read any of them in Compose; predict cannot see a panel's geos, so the run judges a per-geo term. In ROI mode `tv` is the β that the population ROI and kernel give over the whole panel. `[` and `]` are refused in a predictor's name (a migration break), and `tau:` on a panel.
- **Outputs.** `roi`, `mroi` and `contribution` keep `<channel>` for the all-geo figure (the panel's summed contribution over its summed spend, a spend-weighted mROI, the summed contribution) and add `<channel>[<geo>]` per geo. `outputs.hierarchy` carries `geo`, `geos` and, per pooled term, `parameter`, `mu` and `tau`.
- **Lift tests.** On a panel each test names its `geo` (required: `lift_geo_missing`, judged by predict too). Its Δ runs on that geo's spend, β, kernel and curve. A geo the panel does not hold is `lift_geo_unknown` (`details.geos`), and a window outside that geo's fitted periods `lift_window_out_of_range` (`details.geo`), both at run time, before any sampling.
- **Response.** `response(fit, channel, spend)` on a panel is the population's curve (the bare μ terms). `response(fit, channel, spend, geo)` is that geo's: its β, its kernel parameters on the channel's adstock record, and its own curve under per-geo curves. A geo the fit does not name is `PULSE_MODEL_TERM_UNKNOWN` with reason `geo_unknown` and the fit's geos under `details.geos`. Predict raises it when the fit and geo are literals and the fit is known to have no geos (any fit but a geo panel); a panel's geos are the data's, so the run judges them.
- **Holdout.** `holdout_from` cuts every geo at the same period, and must leave each geo a fitted row.

**Default priors.** The decay prior is Beta(1, 1), uniform on (0, 1). A Beta(1, 3) pulls toward fast decay and, on the ridge decay shares with the half-saturation and the effect, biases a long-memory channel's ROI; uniform lets the data place it, and a practitioner with a belief sets `priors.<channel>.decay`. Hill shape s is LogNormal(0.3, 0.5) (median 1.35, covering concave and S-shaped curves); the half-saturation k is TruncNormal(1, 1, 0.02, 10) on the adstock scaled by peak spend; ROI is LogNormal(0.2, 0.9); a `beta` prior is HalfNormal(1). Intercept, controls and noise are weakly informative on the max-scaled data.

**Adstock kernels.** `params.adstock` picks one kernel for every channel, each `WIN_DISTRIBUTED_LAG`'s kernel with `normalize: false`, so the model's adstock is that window's column bit for bit:

- `geometric` (default): `w_l = decay^l`, coefficient `decay:<channel>`.
- `delayed` (Jin et al. 2017): `w_l = decay^((l − peak)²)`, the geometric kernel peaking `peak` lags after the spend; coefficients `decay:<channel>` and `peak:<channel>`. Default peak prior TruncNormal(1, 2, 0, `max_lag`); a `priors.<channel>.peak` must stay inside `[0, max_lag]` (a beta, or a truncnormal bounded there).
- `weibull_cdf` (`w_l = exp(−(l/scale)^shape)`) and `weibull_pdf` (the Weibull density at `l + 1`): coefficients `shape:<channel>` and `scale:<channel>` (in periods). Default priors shape LogNormal(0, 0.5), scale LogNormal(1, 0.75).

Every kernel but `geometric` needs `params.max_lag`. A prior key the kernel does not have (`peak` on geometric, `decay` on a Weibull kernel) is refused, and a predictor collides with the chosen kernel's coefficient prefixes. Each kernel's gradient is analytic, checked against finite differences, and each is calibrated (SBC) and pinned to the conjugate limit in the heavy tier.

**Saturation curves.** `params.saturation` picks one curve for every channel, on the adstock `a`; each is 0 at zero spend and rises to 1, so the channel's coefficient is its effect at full saturation:

- `hill` (default): `1 / (1 + (k/a)^s)`, coefficients `k:<channel>` (raw adstock units) and `s:<channel>`.
- `logistic`: `(1 − e^(−λa)) / (1 + e^(−λa))`, coefficient `lam:<channel>` (per raw adstock unit), half-saturated at `log 3 / λ`. Default prior LogNormal(0, 1) on the adstock scaled by peak spend: a median curve half-saturated near one peak week, like Hill's k.
- `michaelis_menten`: `a / (k + a)` (Hill at `s = 1`), coefficient `k:<channel>`, with Hill's k prior.

A prior key the curve does not have (`lam` on Hill, `s` on Michaelis–Menten) is refused with reason `prior_not_in_curve`, and a predictor collides with the chosen curve's prefixes only. The fit carries its curve, so `response()` evaluates it (an inline model declares a non-Hill curve as `"features": ["saturation"], "saturation": "logistic"`). The `hill()` expression function stays the Hill formula. Every kernel × curve cell is calibrated (SBC) and pinned to Stan posterior means (`make reference-stan`) in the heavy tier. The skill `mmm-bayes-model` tabulates kernels, curves and priors.

**Lift tests.** A measured experiment enters as data through `params.lift_tests`, a list of `{channel, start, end, spend_delta, lift, se}`. Each adds `lift ~ Normal(Δ, se)` to the likelihood, where Δ is the model's own change in the outcome over periods `start..end` (`params.time`, inclusive) when the channel's spend there changes by `spend_delta`. Δ re-runs the channel's adstock and curve at every draw's parameters and sums the change over the window only; the carry-over tail past `end` is excluded, as an experiment's readout window excludes it. The gradient includes the term, so the MAP start and NUTS both see it.

- `spend_delta` is the total spend change over the window, spread in proportion to the channel's spend in each period (evenly over a window that never spent). Minus the window spend switches the channel off exactly. It is required and never 0, and a cut below zero spend is refused (`lift_spend_negative`).
- `lift` (the outcome with the change minus the outcome without it, so a switch-off lift is negative) and `se > 0` are in outcome units. Convert a ratio-scale result (iROAS) yourself.
- The window must lie inside the fitted periods, before `holdout_from` (`lift_window_out_of_range`, judged at run time). `geo` is refused on one market's series (`lift_geo_unsupported`) and required on a geo panel (`lift_geo_missing`; above). The spec refusals (`lift_channel_unknown`, `lift_window_missing`, `lift_window_invalid`, `lift_spend_delta_missing`, `lift_spend_delta_zero`, `lift_missing`, `lift_se_invalid`, `lift_geo_unsupported`, `lift_geo_missing`) are `PROCESSING_CONFIG` with `details.slot` `params.lift_tests`, `details.index` and `details.reason`, and predict raises them identically.

The other route is an informative `priors.<channel>.roi`. That prior is only as good as the match between the experiment and the model's ROI (window, geography, definition of incrementality); Meridian's documentation warns of the same mismatch. The likelihood route avoids it: Δ is the experiment's own estimand. Use one route per experiment. A channel with both an `roi` prior and a lift test warns `PULSE_MMM_LIFT_PRIOR_OVERLAP` (`details.channels`), because a prior built from the same experiment counts its evidence twice. Under `Strict` that warning fails the slot. The likelihood is pinned to Stan posterior means and calibrated (SBC) in the heavy tier. See `mmm-workflow`.

**Reading a fit.** `outputs.diagnostics` carries max R̂, minimum bulk and tail ESS, divergences and tree-depth hits. A fit that fails them still returns, with a `PROCESSING_REGRESSION_DIAGNOSTIC` warning whose `details.reason` is `rhat_high`, `ess_low`, `divergences` or `treedepth_saturated`; under `Strict` the slot fails with that code. A 2 x 100-draw run always warns `ess_low`. mROI is analytic with the fitted curve held fixed. k and s are individually unidentifiable (the same ridge as the Hill model): judge ROI and contribution.

**Raw draws.** `params.return_draws: true` adds `outputs.draws` for a posterior analysis of your own (a probability such as P(ROI > 1), a contrast between channels): `names` (the coefficient table), `draws[chain][coefficient][draw]` on the constrained scale in original units, `roi` (each `outputs.roi` entry's draws, `[chain][draw]`, per-geo entries included on a panel) and `thin`. `params.draws_thin: k` keeps every k-th retained draw of each chain (from draw 0); it is refused without `return_draws`. With `draws_thin` 1, the draws pooled chain order then draw order summarise to exactly `outputs.params` and `outputs.roi`. Neither key moves the derived seed, so asking for draws never changes the fit. A non-finite draw is `null` on the wire and reads back as NaN. `Options.Limits.MaxMMMDrawValues` (default 10,000,000) caps chains x kept draws x (coefficients + roi entries); over it the fit is refused with `PULSE_LIMIT_EXCEEDED` before sampling, never truncated, and predict reports the same figure (`certain` on one market, `possible` on a geo panel). The `standard` `return` preset (the MCP default) keeps `outputs`, so the draws arrive exactly when asked for; `exclude: ["regressions[*].outputs.draws"]` drops them alone.

**Budget over the posterior.** A Compose `optimize` block reads a `REG_MMM_BAYES` fit's posterior means through `response()` and `coef()`. Add `posterior` to optimise over its draws instead (the fit needs `return_draws: true`, or load a saved file that kept them): the objective is evaluated per draw and the block maximises their mean, or with `{"objective": "quantile", "q": 0.1}` their 10% quantile, a plan that holds up in nine draws of ten. Per draw `response()` reads that draw's β, curve and decay (with a geo argument, that geo's own). Constraints hold on their mean over the draws; bounds and starts read the means. `draws: N` evaluates a seeded subsample. Each variable adds `interval`, the `credible_level` range of its best value when the block is re-solved on each draw alone: how far the posterior moves the plan. The block must read exactly one fit, a Bayes fit with draws; anything else is refused by predict and the run alike (`PULSE_OPTIMIZE_SPEC_INVALID`). Example: `compose/15_posterior_budget.json`; skills `compose-optimize`, `mmm-bayes-posterior`.

**Interval width.** An always-on channel is the hardest to identify, so its interval is wide: read the width of each channel's interval, not only its centre. Pulse publishes no calibration figure for the intervals; judge a fit by its `diagnostics` and by whether the intervals you rely on are tight enough to act on.

**Cost.** A default fit runs 4 chains x 2000 NUTS iterations, so it is usually the slowest slot in a request. Same request and seed give the same bytes on any worker count and architecture.

## Test

```
go test ./internal/regmmm/
```

The models' operator tests (params, outputs, refusals, predict/runtime parity, determinism, registration) and FMA gates live with them in `internal/regmmm`. Like every operator, the models are tested one operation at a time; the recipe on this page is documentation, not a test.
