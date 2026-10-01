# 01 — First-class weighting

## Request surface

```jsonc
{
  "weight": { "field": "wt", "kind": "frequency" },   // request-level default for every weight-aware slot
  "aggregations": [
    { "type": "AGG_FREQUENCY", "field": "region" },                  // weighted by "wt"
    { "type": "AGG_COUNT", "label": "unweighted_n", "weight": null }  // explicit opt-out per slot
  ]
}
```

- **`Request.Weight`** (additive, `omitempty`) sets the default weight for every weight-aware slot in the request.
- **Per-slot `weight`** overrides it: another field name, or `null` to force an unweighted figure. The common need is to report an **unweighted base next to weighted percentages**. Crosstabs already have `margin_aggregations` for exactly that, and per-slot `null` covers the rest.
- **`kind`:**
  - `frequency`: a weight of 3 means "3 identical rows".
  - `probability` (default for SPSS imports that carry a weight variable): a survey/design weight. Point estimates are identical under both kinds. **Inference differs**: probability weights use the Kish effective sample size `n_eff = (Σw)² / Σw²` for standard errors, degrees of freedom and test statistics.
- **Instance default.** `Options.DefaultWeight` is for embedders whose cohorts all carry the same weight column. It is off unless set.
- **SPSS.** If the `.sav` file declares a weight variable, import records it in the SPSS sidecar. The manifest/inspect output names it as the suggested weight. It is never applied silently; this is the hook for the guided-analysis advisory.

## Weight validation

| Weight value | Behaviour |
|---|---|
| positive finite | used |
| zero | row contributes nothing (legitimate, e.g. out-of-quota) |
| null, negative, NaN, ±Inf | row excluded from weighted figures. Counted in `Components` as `n_weight_invalid`, with one warning `PULSE_WEIGHT_INVALID_ROWS` per request carrying the count. Never coerced silently. |
| weight field not numeric | `PROCESSING_CONFIG` at validate / predict time |

## What becomes weight-aware

| Family | Weighted semantics | Notes |
|---|---|---|
| `AGG_COUNT`, `AGG_FREQUENCY`, `AGG_SUM`, `AGG_AVERAGE`, `AGG_VARIANCE` / `STDDEV` / `WELFORD`, `AGG_PERCENTILE` / `MEDIAN`, `AGG_RATIO` | standard weighted estimators; weighted percentiles use the weighted-CDF definition (documented, deterministic tie rule) | `AGG_WEIGHTED_MEAN` becomes an alias of weighted `AGG_AVERAGE`, kept for compatibility |
| `AGG_SET_*` | weighted member frequencies | |
| `AGG_DISTINCT_*`, `AGG_MIN` / `MAX` / `RANGE` / `MODE` | distinct counts and extremes are not weightable; refused with `PROCESSING_CONFIG` when a weight is explicitly set; the request-level default skips them silently, which is documented | |
| Crosstab cells and margins | weighted cells; margins recomputed from weighted raw rows (same rule as today) | the unweighted base rides `margin_aggregations` with `weight: null` |
| Share / index overlays | computed on weighted figures | |
| Significance overlays (`PROP_Z`, `T`, `Z`, pairwise, χ²) and `TEST_*` | weighted estimates; under `probability` weights, `n_eff` replaces n | the overlay summary carries `n_eff` |
| `ATTR_ZSCORE`, `ATTR_PERCENTILE`, `ATTR_NORMALIZED` | weighted reference distribution | |
| `REG_*` | weighted least squares / weighted IRLS | already structurally possible in the accumulator |
| `GROUP_QUANTILE` | weighted quantile boundaries | |
| Windows (`WIN_*`) | **not weight-aware in v1.0.0**; refused when a weight is explicitly set | rolling weighted statistics are post-1.0 |

**Out of scope:** design-based variance, i.e. stratification, clustering, replicate weights and Taylor linearization. Kish `n_eff` is the documented approximation. The stability policy notes that adding full design-based variance later would be a new opt-in `kind`, not a change to existing results.

## Execution & contract

- **Mergeability.** Weighted sums, means and co-moments merge exactly, so streaming, shards and parallel decode are preserved. Weighted percentiles stay buffered (as unweighted percentiles are today).
- **Components.** The universal floor gains `w_sum` and, when `kind` is probability, `n_eff`. The extension `ComponentSchema` docs explain how custom operators read the weight.
- **Extensions.** Aggregator, attribute and test registrations receive the resolved weight accessor. A registration declares `WeightAware bool`. A non-aware extension explicitly given a weight is refused with `PULSE_EXTENSION_NOT_WEIGHT_AWARE`; under the request-level default it is skipped silently.
- **Predict / manifest.** Predict reports, per slot, whether a weight applies and which one. The manifest marks each operator `weight_aware`.
- **Profiles.** `capability:weighting` is a feature like any other.

## Gates

- **`TestWeightUnityParity`:** for every weight-aware operator, a weight column of all 1.0 gives byte-identical results to unweighted. This is the cheapest strong correctness check available.
- **`TestWeightFrequencyExpansionParity`:** an integer frequency weight equals the result of physically duplicating rows.
- **Weighted-vs-reference fixtures** for percentages, means, the t-test and χ², checked against published worked examples.

## Deliverables

- [ ] `Request.Weight` + per-slot `weight` (incl. `null` opt-out) + `Options.DefaultWeight`
- [ ] Weight validation, `n_weight_invalid`, `PULSE_WEIGHT_INVALID_ROWS`
- [ ] Weighted aggregators (incl. weighted percentiles) and the `AGG_WEIGHTED_MEAN` alias
- [ ] Weighted crosstab cells and margins; unweighted base via `margin_aggregations`
- [ ] Weighted share / index overlays
- [ ] Weighted tests and significance overlays, with `n_eff` for probability weights
- [ ] Weighted attributes, `GROUP_QUANTILE`, regressions
- [ ] Components `w_sum` / `n_eff`; manifest `weight_aware`; predict reporting; extension `WeightAware`
- [ ] SPSS weight-variable capture and suggestion
- [ ] Unity and frequency-expansion parity gates; reference fixtures
- [ ] Skills: `## Params` updated per operator; new topical skill `weighting.md`
