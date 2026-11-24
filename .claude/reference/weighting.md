# Weighting — weighted descriptive and inferential figures

**How to load.** This file is an INDEX: the preamble below plus a slice map. Load it, then ONLY the slice(s) under `.claude/reference/weighting/` your change touches; find one with `grep -l '<identifier>' .claude/reference/weighting/*.md`. A section-qualified pointer at this file resolves through its slices.

The row-weighting contract. Load it before touching ANY weight surface: `types.Request.Weight`, a per-slot `weight`, `pulse.Options.DefaultWeight`, the resolver or validation pass, an operator's weight class or manifest `weight_kinds`, a refusal, a weighted inferential formula, the `sum_weights` / `n_eff` / `n_weight_invalid` floor keys, `PULSE_WEIGHT_LOW_NEFF`, the extension `WeightAware` contract, the SPSS weight suggestion, `Pulse.Rake`, or `capability:weighting`. Agent-facing summary: `skills/weighting.md`; user guide: `docs/src/library/weighting.md`. When code and this file disagree, the change that moved the code fixes this file in the same PR.

Three rules hold everywhere:

- **Null ≠ absent.** An absent slot `weight` inherits; `null` opts the slot out.
- **A class flips only with its computation.** An operator becomes weight-aware in the change that lands its weighted form — earlier, it would silently run unweighted.
- **Predict ≡ runtime.** One resolver (`ResolveWeights`) decides every slot for both, so refusals match code, message and details.

`format_version` stays `"1.1"` for every weight surface: each addition is additive `omitempty` / `omitzero`, and a request that carries no weight produces byte-identical output.

## Slice map

| Slice | Covers |
|---|---|
| `weighting/surface-and-classification.md` | the request / slot / option surface, null ≠ absent, weight field type and existence, resolution order, threading (the weight rides the spec), validation and the invalid-rows warning, classification by family |
| `weighting/aggregators-and-crosstab.md` | weighted meaning of each aggregator, weight kinds and manifest `weight_kinds`, weighted quantiles (Hmisc, integer snap), mode, moments, counting aggregators, the decimal128 refusal, both crosstab arms |
| `weighting/refusals-and-inference.md` | refusal rules and details, overlay / Compose / facet weighting, the one rule (N*, w*), minimum-n floors, what Kish is and is not, n_eff output keys, tests and regressions incl. JK1 and influence |
| `weighting/inference-multivariate.md` | scores and quantile buckets, the formula total, CUPED, multivariate tests, fitted scores, the low-n_eff warning, CI aggregators, matrix operators |
| `weighting/overlays.md` | mean / contingency / proportion overlays on a weighted host, `n_source`, components-disabled hosts, the weighted two-means host |
| `weighting/exactness-and-fixtures.md` | floor keys, the operation-order exactness rule and parity gates, the reference-fixture oracle policy |
| `weighting/rake.md` | `Pulse.Rake`: computing rim / cell weights into a new cohort |
| `weighting/extensions-and-codes.md` | extension `WeightAware` contract, SPSS suggestion, feature profile, error codes |
