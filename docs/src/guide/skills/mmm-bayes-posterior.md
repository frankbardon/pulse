```yaml
name: mmm-bayes-posterior
description: REG_MMM_BAYES raw posterior draws — params.return_draws and params.draws_thin, the outputs.draws shape (names, draws per chain, per-draw ROI), how the draws relate to the reported summaries, null draws, the Limits.MaxMMMDrawValues cap and response shaping — saving and scoring a fit over its draws (pre-adstocked or carried inputs, credible bounds, unseen geos, the two holdouts), a budget optimised over the draws (optimize posterior), and choosing the sampler (nuts or static hmc, path_length, step_size).
kind: design
type: guide
applies_to: process, compose, predict
requires: [REG_MMM_BAYES]
covers: [posterior draws, return_draws, draws_thin, thinning, per-draw ROI, draws limit, save, scoring, pre-adstocked inputs, credible bound, unseen geo, engine holdout, optimize posterior, allocation interval, sampler, hmc, path_length, step_size]
```

# Bayesian MMM: raw posterior draws

`REG_MMM_BAYES` ([`op-reg-mmm-bayes`](op-reg-mmm-bayes.md)) summarises its posterior in `outputs`. Ask for the draws when a summary is not the question: a probability (P(ROI > 1)), a contrast between channels, a custom interval.

## Asking for draws

`params.return_draws: true` adds `outputs.draws`; `params.draws_thin: k` (an integer 1–20000, default 1) keeps every k-th retained draw of each chain (draw 0, k, 2k, …), so a chain returns ⌈draws / k⌉. `draws_thin` without `return_draws` is `PROCESSING_CONFIG` reason `draws_thin_without_return_draws`. Neither key moves the derived seed, so the posterior is unchanged.

## Shape

```json
{"names": ["(intercept)", "tv", "price", "decay:tv", "k:tv", "s:tv", "sigma"],
 "draws": [[[...], ...], ...],
 "roi": {"tv": [[...], ...]},
 "thin": 1, "credible_level": 0.9}
```

- `credible_level` is the fit's (a scoring bound reads it).
- `draws[chain][i][d]`: coefficient `names[i]` at kept draw `d`, on the constrained scale in original units — the values `outputs.params` summarises.
- `roi[<entry>][chain][d]`: each `outputs.roi` entry's draw; on a geo panel also `<ch>[<geo>]`, and `names` is the panel's table (`tv[NE]`, `tau:tv`, …; [`mmm-bayes-model`](mmm-bayes-model.md)).
- Pool chains in chain order then draw order: with `draws_thin` 1 the pooled draws summarised reproduce `outputs.roi` and `outputs.params` exactly.
- A non-finite draw is `null` on the wire; decoded into a float slot it reads back as NaN, never 0.

## Size limit

The payload is chains × kept draws × (coefficients + roi entries) values, capped by `Limits.MaxMMMDrawValues` (`max_mmm_draw_values` in the manifest `limits`). Over it the fit is refused with `PULSE_LIMIT_EXCEEDED` before any sampling, never truncated: raise `draws_thin` or the limit. Predict reports the same figure: `certain` (and the same refusal) on one market, `possible` on a geo panel, whose count grows with the geos it reads.

## Response shaping

`outputs` is kept by the `standard` `return` preset (the MCP default), so the draws arrive exactly when `return_draws` asked for them. `exclude: ["regressions[*].outputs.draws"]` drops them from the wire alone.

## Saving and scoring

A fit scores over its draws, so `save`, the engine `holdout` and scoring it ([`op-attr-reg-predict`](op-attr-reg-predict.md), a `params.fit` slot) need `return_draws: true`; without it they are refused `draws_required` (`PROCESSING_CONFIG`; scoring `PULSE_MODEL_SCORE_INVALID`), in predict too. The file keeps the thinned draws even when `exclude: ["regressions[*].outputs.draws"]` drops them from the wire.

- A score carries the fit's adstock into raw spend (`params.order`, [`regression-order`](regression-order.md)). Under `carryover: false` inputs are pre-adstocked: build each channel with `WIN_DISTRIBUTED_LAG` from the file's `adstock.<ch>` record (posterior-mean params, `normalize: false`, `fixed_at: "posterior_mean"`). Either way the decay's uncertainty is not in the score.
- The score is the mean over the draws of α + Σγ·z + Σβ·curve(x). Whole-model `bound: "lower"` / `"upper"` (no `terms`) is the credible bound of that mean at `credible_level`: an interval for μ, not a predictive interval (no σ).
- A geo panel's scoring frame needs the `geo` field; each row uses its geo's intercept, β and curve. A geo the fit lacks scores from the population (each channel's μ β and curve, the mean geo intercept) and warns `PULSE_MMM_GEO_UNSEEN` (`geos`, `rows`, `fit_geos`).
- Two holdouts: `params.holdout_from` adstocks every period at each draw's decay, the fit seeing those before it. The engine `holdout` fits the train rows and carries their adstock (posterior-mean kernel) into the held-out rows; `carryover: false` scores their columns as given.

## Optimising over the draws

A Compose `optimize` block's `posterior: {objective, q, draws}` ([`compose-optimize`](compose-optimize.md)) evaluates its objective once per draw and optimises their `mean` (default) or `quantile` `q` (type 7, as the credible intervals; maximise a low `q` for a cautious plan); each constraint holds on its mean over the draws, while bounds and starts read the posterior means. `draws: N` takes a seeded subsample (the block's `rng_seed`); 0 is every draw. Per draw, `response()` reads that draw's β, curve and decay; with a `geo` argument that geo's own draws, without one the population's.

- The objective and constraints read exactly one fit by literal name: a Compose fit with `return_draws: true`, or a `models` file that kept its draws. Else `PULSE_OPTIMIZE_SPEC_INVALID` reason `posterior_fit_missing`, `posterior_fits_multiple`, `posterior_fit_not_bayes` or `draws_required`, in predict too.
- Each variable adds `interval`: the `credible_level` range of its optimum when the block is re-solved on each draw alone (one start, at the reported optimum). `posterior` echoes `fit`, `draws`, `draws_available` and `draws_resolved` (re-solves that converged; the interval is over those).
- Cost grows with the draws: subsample a long run.

## Choosing the sampler

NUTS (`params.sampler` `nuts`, the default) tunes each trajectory itself; keep it unless you need a fixed cost per draw. `hmc` is static HMC: every transition runs ⌈`path_length` / step size⌉ leapfrog steps, so `path_length` is required (refused under `nuts`, as is `step_size`).

- Start at `path_length` π/2 (1.5708): on the adapted unit-scale metric a quarter period moves a draw to a nearly independent point. Stan's 2π brings it back near its start; much shorter paths random-walk. Read `ess_bulk` / `ess_tail` and lengthen the path when they are low.
- Omit `step_size` to adapt it towards `target_accept`; a fixed `step_size` turns adaptation off (`target_accept` beside it is refused). Divergences: lower it. Adapting, a warmup path stops at 1023 steps (NUTS's cap); draws run the full path.
- No tree: `diagnostics` has no `treedepth_saturated` / `max_tree_depth` and that warning never fires.

## See

[`op-reg-mmm-bayes`](op-reg-mmm-bayes.md) · [`mmm-bayes-model`](mmm-bayes-model.md) · [`response-shaping`](response-shaping.md) · [`compose-optimize`](compose-optimize.md)
