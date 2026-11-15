---
name: compose-post-fit
description: ComposedRequest.post_fit — run an embedder-registered POSTFIT_<NS>_<NAME> operator over a batch's finished fits (a response-curve table, a marginal-return grid, a scenario table) and read its scalars and tables from the post_fit root. How to find the instance's operators, the entry shape and phase, what an operator reads, the failures, and what Explain says.
kind: design
type: guide
applies_to: compose, predict
covers: [PostFitSpec, PostFitResult, PostFitTable, post_fit, post-fit operator, POSTFIT, marginal-return grid, response-curve table]
requires: [capability:compose_post_fit]
---

# Compose post-fit

A **post-fit entry** runs a computation over models the batch has already fitted — a grid of marginal returns per spend level, a response-curve table, a scenario table over an optimize solution — and returns named scalars and named tables. It reads no record.

**Every post-fit operator is the embedder's.** Pulse ships none, so the set is the instance's: `pulse_manifest` lists each under `extensions.post_fits` with its `params` and its declared `outputs` (scalar names; tables with their columns and cell kinds), and the payload schema's `post_fit` `type` enum names the same set — empty on an instance without any. A name outside it is refused, never guessed at.

```jsonc
{
  "requests": [{"label": "spend", "...": "a slot whose aggregation `mean_x` is the mean weekly spend"}],
  "fits": [{"name": "mmm", "request": {"...": "one media-mix regression"}}],
  "post_fit": [{
    "label": "mroi", "type": "POSTFIT_ACME_MROI", "fits": ["mmm"],
    "params": {"channel": "x", "spend": [20, 40, 60], "reference_slot": "spend", "reference_aggregation": "mean_x"}
  }]
}
```

## The entry

- `label` (required, unique among the batch's entries) — echoed on the result.
- `type` (required) — a registered `POSTFIT_<NAMESPACE>_<NAME>`.
- `fits` — the `fits` entries the operator reads, each a regression fit named once. The operator sees only these.
- `params` — the operator's own object, checked against its declared params exactly as an extension operator's slot params are.

## Phase and reads

The batch runs slots and sweeps → fits → optimize → **post-fit** → nested-model comparisons. Each entry runs once. The operator reads only:

- each named fit: coefficients and standard errors by term, the `vcov` matrix when the fit computed one (`vcov: true`), each channel's adstock record (kernel, parameters, lag weights) and a Bayesian media-mix fit's pooled posterior draws when it ran with `params.return_draws`;
- any ungrouped slot value, by the `result(slot, aggregation)` reader's rules;
- every optimize block's solution (objective, variables, convergence);
- its params.

So a post-fit entry is where "what does the fitted model imply?" goes when an expression cannot say it: a whole table per call, many points per curve, figures read off the posterior draws.

## The result

`post_fit[i]` of the response answers `post_fit[i]` of the request:

```jsonc
"post_fit": [{"label": "mroi", "type": "POSTFIT_ACME_MROI",
  "scalars": {"beta": 7.99, "mroi_at_reference": 0.098},
  "tables": {"grid": {"columns": ["spend", "response", "marginal_roi", "vs_half_saturation"],
    "rows": [[20, 3.16, 0.192, "below"], [40, 5.79, 0.0799, "above"]]}}}]
```

- The scalars and tables are exactly the ones the manifest declares — every one present, nothing else. A table's `columns` are in declared order and each row holds one cell per column: a number in a float column, a string in a string column, or `null`.
- An undefined figure is `null`, never `0`.
- The root is outside the Compose `return`: always emitted whole, at full precision; a `return` path naming `post_fit` is `PULSE_RETURN_INVALID`.
- What a figure MEANS is the operator's to say: read its manifest `description` and intents; Pulse judges no inference on it.

## Failures

| Fault | Code | Effect |
|---|---|---|
| missing / repeated `label`, missing or unregistered `type`, a `fits` name empty, repeated, unknown or not a regression fit | `PULSE_POSTFIT_INVALID` (`details.reason`, `details.post_fit`) | batch refused before anything runs; predict says the same |
| an undeclared params key / a wrong type or a missing required key | `PULSE_REQUEST_UNKNOWN_FIELD` / `PULSE_REQUEST_PARAM_INVALID` at `post_fit[i].params` | batch refused; predict says the same |
| a named fit failed on its data, or the operator returned an error | the fit's code, the operator's code, else `PULSE_POSTFIT_FAILED` | the entry alone fails: `{label, type, error}`, warning `PULSE_SLOT_FAILED` (`slot_kind` `post_fit`); `strict` fails the batch with that code |
| the operator returned output its declaration does not admit | `PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH` | fatal: an embedder bug, not your request |

`pulse_errors_lookup` gives each code's fix. Predict runs every request rule above without running the operator, so check a batch there first.

<!-- feature: capability:explain -->
## Explain

`pulse_explain` describes each entry as a `post_fit[i]` step — operator (with its purpose sentence when the embedder declared one), label, the fits it reads, its params and the phase — and puts every post-fit refusal at that slot. Reading a response, each entry is a descriptive finding at `post_fit[i]` with numbers `scalars.<name>` and `tables.<name>.rows`; a failed entry is `not_computable` with its code in a caveat.
<!-- /feature -->

## See

`compose-fits` (the fits an entry reads) · `compose-optimize` (the solutions it reads; a single best setting is an optimize block, not a post-fit entry) · `compose-requests` (slots and `result()`) · `tool-explain`.
