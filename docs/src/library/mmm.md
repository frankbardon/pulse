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
    "objective": "coef(\"mmm\", \"tv_ad\") * hill((1 - sweep_axis(\"mmm\", \"tv\") ^ 14) / (1 - sweep_axis(\"mmm\", \"tv\")) * tv, coef(\"mmm\", \"k:tv_ad\"), coef(\"mmm\", \"s:tv_ad\")) + ...",
    "constraints": [{"expr": "tv + search - params.budget", "type": "eq"}]
  }]
}
```

- **The sweep** is an `lhs` search with one decay axis per channel, each a `range` (TV-like `[0.3, 0.8]`, digital `[0, 0.4]`; keep the maximum below 1). Each slot is one `WIN_DISTRIBUTED_LAG` per channel and one `REG_MMM_HILL` with a `holdout`, ranked by holdout RMSE. In one recorded measurement on four channels, about 400 samples did not recover decay and ROI reliably; 2,000 did. The winning decays are `ranking[0].axes`, as typed numbers.
- **The bound fit** `fits[].from_sweep` refits the winning slot's regression, holdout kept, under the name `mmm`. Explicit slots and `optimize` read it with `coef("mmm", …)` (see the Compose sweeps page).
- **The contribution slot** cannot set a window's `decay` from the winner, because window params are literals. Instead it rebuilds each adstock: one `WIN_LAG` item fanned out with `for_each` over channel × lag `1..max_lag` (missing history is `0`), then a formula `x + d·(x_l1 + d·(x_l2 + …))` with `d = sweep_axis("mmm", "<channel>")`. That is the same sum `WIN_DISTRIBUTED_LAG` computes, so the result matches it to rounding. A contribution is `coef · hill(adstock, k, s)`.
- **The optimize block** maximises the summed curves at steady state. The multiplier for a constant weekly spend of 1 is `(1 − d^L) / (1 − d)` with `L = max_lag + 1`, read from `sweep_axis`. It runs under `sum(spend) = budget`, each channel bounded (say 0.5 to 1.5 × current). Current spend, bounds and budget are the caller's figures in `params`.
- **Slots.** `MaxComposeSlots` counts the sweep's samples plus the explicit slots, so 2,000 samples and the contribution slot need 2,001. Raise `Options.Limits.MaxComposeSlots`, or pass `pulse --limit max_compose_slots=2001 api compose --request mmm.json --json` on the CLI.

The batch runs the sweep slots first, then the rank, the bound fit, `optimize` and the contribution slot. Responses keep index order, so the contribution slot comes first. Serial and parallel runs give the same bytes. A pinned `rng_seed` (or the echoed `sweep_seed`) replays the draws.

At the optimum every free channel's `marginal` (its marginal ROI) equals the budget `multiplier`. A channel on a bound sits above or below it. Trust the answer by `converged` and `starts_agreeing`. One measurement on a synthetic four-channel cohort with known truth (156 weeks, 2,000 samples, 13 sweep seeds) gave: decay error ≤ 0.10, ROI error ≤ 9%, free-channel marginal ROI equal to the multiplier within 1e-5, lift error ≤ 12%, and 8 of 8 starts agreeing.

Skills carry the detail: `mmm-workflow` (the flow), `op-reg-mmm-hill` (the model), and the core `compose-sweep-bind`, `compose-sweep-sampling` and `compose-optimize`.

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

**Calibration.** A 50-replicate coverage study, run once when the model shipped (fresh data each, 90% intervals), covered the true ROI and contribution 92% of the time overall (target band 80-98%): tv 86%, search 92%, display 90%, social 100% with wide intervals (about 13.6% of the true ROI, against 2.3% for tv) because an always-on channel is the hardest to identify. Median ROI bias was at most 0.5%, with no divergences and max R̂ 1.0047. It is a recorded measurement, not a test.

**Cost.** About 32-39 s per default fit (4 chains x 2000 iterations) on a laptop-class arm64 core. Same request and seed give the same bytes on any worker count and architecture.

## Test

```
go test ./internal/regmmm/
```

The models' operator tests (params, outputs, refusals, predict/runtime parity, determinism, registration) and FMA gates live with them in `internal/regmmm`. Like every operator, the models are tested one operation at a time; the recipe on this page is documentation, not a test.
