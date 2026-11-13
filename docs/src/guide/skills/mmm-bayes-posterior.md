```yaml
name: mmm-bayes-posterior
description: REG_MMM_BAYES raw posterior draws — params.return_draws and params.draws_thin, the outputs.draws shape (names, draws per chain, per-draw ROI), how the draws relate to the reported summaries, null draws, the Limits.MaxMMMDrawValues cap and response shaping.
kind: design
type: guide
applies_to: process, compose, predict
requires: [REG_MMM_BAYES]
covers: [posterior draws, return_draws, draws_thin, thinning, per-draw ROI, draws limit]
```

# Bayesian MMM: raw posterior draws

`REG_MMM_BAYES` ([`op-reg-mmm-bayes`](op-reg-mmm-bayes.md)) summarises its posterior in `outputs`. Ask for the draws themselves when a summary is not the question: a probability (P(ROI > 1)), a contrast between channels, a custom interval.

## Asking for draws

`params.return_draws: true` adds `outputs.draws`; `params.draws_thin: k` (an integer 1–20000, default 1) keeps every k-th retained draw of each chain (draw 0, k, 2k, …), so a chain returns ⌈draws / k⌉. `draws_thin` without `return_draws` is `PROCESSING_CONFIG` reason `draws_thin_without_return_draws`. Neither key moves the derived seed: the posterior is the one you get without them.

## Shape

```json
{"names": ["(intercept)", "tv", "price", "decay:tv", "k:tv", "s:tv", "sigma"],
 "draws": [[[...], ...], ...],
 "roi": {"tv": [[...], ...]},
 "thin": 1}
```

- `draws[chain][i][d]`: coefficient `names[i]` at kept draw `d`, on the constrained scale in original units — the values `outputs.params` summarises.
- `roi[<entry>][chain][d]`: each `outputs.roi` entry's draw; on a geo panel also `<ch>[<geo>]`, and `names` is the panel's table (`tv[NE]`, `tau:tv`, …; [`mmm-bayes-model`](mmm-bayes-model.md)).
- Pool chains in chain order then draw order: with `draws_thin` 1 the pooled draws summarised reproduce `outputs.roi` and `outputs.params` exactly.
- A non-finite draw is `null` on the wire; decoded into a float slot it reads back as NaN, never 0.

## Size limit

The payload is chains × kept draws × (coefficients + roi entries) values, capped by `Limits.MaxMMMDrawValues` (`max_mmm_draw_values`, default 10,000,000). Over it the fit is refused with `PULSE_LIMIT_EXCEEDED` before any sampling, never truncated: raise `draws_thin` or the limit. Predict reports the same figure: `certain` (and the same refusal) on one market, `possible` on a geo panel, whose count grows with the geos it reads.

## Response shaping

`outputs` is kept by the `standard` `return` preset (the MCP default), so the draws arrive exactly when `return_draws` asked for them. `exclude: ["regressions[*].outputs.draws"]` drops them from the wire alone.

## See

[`op-reg-mmm-bayes`](op-reg-mmm-bayes.md) · [`mmm-bayes-model`](mmm-bayes-model.md) · [`response-shaping`](response-shaping.md)
