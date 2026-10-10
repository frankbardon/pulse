---
id: U40
slug: compose-sweep
title: "A parameter grid is one compose request: Pulse expands it, runs every combination and ranks the results"
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: [U35]
blocks: [U41]
todo_items: [272, 273, 274, 275, 276, 277, 278, 297, 298]
branch: compose-sweep
---

# U40 — compose-sweep

**Outcome:** A parameter grid is one compose request: Pulse expands it, runs every combination and ranks the results.

**Track:** API & release · **Size:** M · **Depends on:** none · **Soft:** [U35](U35-predict-runtime-parity.md) (predict reports the expansion) · **Unblocks:** [U41](U41-derived-cohort.md)

## Summary

A compose request is a literal list of requests. A grid search (the same request run once for every combination of a few parameter values) therefore needs a script to write the list: the marketing-mix worked example needed 256 slots, one per combination of four adstock decays, to pick the decay for each channel. That one generated file was the only step of an otherwise all-Pulse analysis that needed outside tooling.

This unit adds an optional `sweep` block to `ComposedRequest`. It holds one request body with placeholders, a list of values per placeholder (an **axis**), a label pattern and an optional ranking. Before anything runs, Pulse expands it into one ordinary slot per combination. By default that is the Cartesian product: every combination, product of the axis lengths. Each slot then runs exactly as a hand-written slot would. Sweeps never read other slots' results; chaining one step's output into the next stays `process-chain`'s job. The optional `rank` block sorts the **finished** slot responses by one scalar and returns the best few.

**Source of inspiration:** the claude.ai doc *Pulse for Marketing Mix Modeling: A Worked Scenario*, <https://claude.ai/code/artifact/20642905-0a1d-4d17-acdc-2d06b8580ccd> (maintainer-private; read it with the Artifact tool's `read` action or the Docs connector). Its inputs, requests and verified outputs are checked in at [`fixtures/mmm-harbor-pine/`](../fixtures/mmm-harbor-pine/README.md); `req/06_decay_grid.json` there is the scripted file this unit makes unnecessary, and the fixture README carries the target sweep and the expected numbers.

Numbering note: appended as U40 after U39, not renumbered. This unit was not in the original plan.

## References

**Read before starting:**
- CLAUDE.md "Update Demand", "Output Format Contract" (Compose envelope) and "Request templating"
- `.claude/reference/update-demand.md`: add the per-slot row for `ComposedRequest.Sweep` (and `ComposedResponse.Ranking` if E3 lands)
- `.claude/reference/request-templating.md`: the substitution forms (`$var` slot marker, `{{}}` string sugar), the import ceiling (`TestTemplatePackage_ImportBoundary`), and why render never opens a cohort
- `.claude/reference/architecture.md`: adding a public symbol (`types.SweepSpec`), `TestPublicAPIGolden`
- `.claude/reference/response-components.md` (Opt-out and the Compose surface): the `ComposedResponse` envelope rules
- `.claude/reference/feature-profiles.md`: a new capability row with `Since` and a dependency edge on `capability:compose`
- `.claude/reference/predict-inspect.md`: predict reports the expansion without executing
- `skills/compose-requests.md`, `skills/tool-compose.md`, `docs/src/library/tuning-limits.md` (`MaxComposeSlots`)

**TODO items delivered by this unit** (#297 and #298 are optional follow-ups filed from the worked example) (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#272** (Compose parameter sweeps) `ComposedRequest.Sweep`: one request body + named axes + a label pattern, expanded before execution into ordinary slots; `grid` (Cartesian, default) and `zip` (paired) modes; deterministic order and labels
- [ ] **#273** (Compose parameter sweeps) Substitution reuses the request-template engine (`{"$var": "axis"}` type-preserving slot markers and `{{axis}}` string sugar); no second placeholder syntax
- [ ] **#274** (Compose parameter sweeps) The expanded slot count, plus any explicit `requests`, is bounded by the existing `Options.Limits.MaxComposeSlots` (default 1,000) and refused with `PULSE_LIMIT_EXCEEDED` before any slot runs; predict reports `axes`, `mode` and `expanded_count`
- [ ] **#275** (Compose parameter sweeps) `sweep.rank {by, order, top}` ranks finished slot responses by one scalar path; `ComposedResponse.Ranking` lists `{label, value, rank}`
- [ ] **#276** (Compose parameter sweeps) New codes with `codeMetadata` and owners: `PULSE_SWEEP_INVALID` (spec faults) and `PULSE_SWEEP_RANK_PATH` (a rank path that does not resolve to exactly one number per slot)
- [ ] **#277** (Compose parameter sweeps) Companions: payload-schema golden, `update-demand.md` row, `features.go` capability row, `compose-requests` / `tool-compose` skills, an examples-library entry, CLAUDE.md Compose-envelope note, `docs/src/library/` page
- [ ] **#278** (Compose parameter sweeps) Acceptance: the MMM fixture's 256-slot grid is reproduced bit-for-bit by one sweep, and its ranking matches
- [ ] **#297** (Follow-ups from U40) Let a following request reference a sweep winner (`rank.top`) instead of typing the winning axis values by hand (worked-example gap 14); decide whether it is a named alias or a ranking reference
- [ ] **#298** (Follow-ups from U40) Per-axis profile in the ranking output (the minimum of the metric over the other axes, per axis value), so "how identifiable is each axis" is a response field, not an eyeball of the grid (worked-example gap 15)

## Proposed wire form

```json
{
  "requests": [],
  "sweep": {
    "axes": [
      {"name": "tv",     "values": [10, 30, 50, 70]},
      {"name": "search", "values": [10, 30, 50, 70]}
    ],
    "mode": "grid",
    "label": "tv{{tv}}_se{{search}}",
    "request": {"features": [{"type": "FEAT_LOG", "field": "tv_ad{{tv}}", "label": "tv_sat"}], "...": "..."},
    "rank": {"by": "regressions.mmm.residual_std_err", "order": "asc", "top": 10}
  }
}
```

- **`axes`**: ordered, non-empty; each `name` unique and an identifier; `values` non-empty scalars (number, string, boolean). Every axis must be referenced by the body or the label, and every placeholder must name an axis, else `PULSE_SWEEP_INVALID`.
- **`mode`**: `grid` (default) expands the Cartesian product, first axis slowest (row-major); `zip` walks the axes in lockstep and requires equal lengths.
- **`label`**: a `{{}}` pattern; default `<axis>=<value>` joined by `_`. Expanded labels go through the existing label-collision check (`PULSE_COMPOSE_LABEL_COLLISION`) together with any explicit `requests`.
- **`request`**: an ordinary `Request` body, strictly decoded after substitution, so a typo fails exactly as it would in a hand-written slot.
- **`rank`** (E3): `by` is a path to one number in each slot's `Response` (proposed grammar: `regressions.<name>.<stat>`, `data.<label>` for an ungrouped aggregation, `post_tests.<label>.<stat>`; names, never indexes), `order` `asc|desc`, `top` optional.

## Scope

**In scope**
- `types.SweepSpec` / `SweepAxis` / `SweepRank` on `ComposedRequest`; expansion in the compose service path before slot dispatch
- Reuse of `internal/template` substitution (exporting a helper if needed, inside the template package's import ceiling)
- Predict for `ComposedRequest`: expansion summary and limit refusal, no execution
- `ComposedResponse.Ranking` (E3), additive `omitempty`
- Library (`pulse.Compose`, `ComposeParallel`), CLI (`pulse api compose`, `--parallel`, `--stream`) and MCP (`pulse_compose`) all accept a sweep with no new leaf or tool
- `--echo-request` shows the expanded `requests`

**Out of scope**
- Sweeps inside `ChainRequest`, `FacetRequest` or `SampleRequest`
- Adaptive search (successive halving, Bayesian optimisation); a sweep is exhaustive by construction
- Sweeping over cohorts as an axis beyond what plain substitution into `cohort.filename` already allows
- Any change to `$when` or the template document model

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(compose-sweep/E<n>-S<m>): …`; close each epic with `milestone(compose-sweep/E<n>): vertical slice complete — <epic title>`.

### E1 — A sweep expands into ordinary slots
- S1: `types.SweepSpec` + payload schema + golden; strict validation and `PULSE_SWEEP_INVALID` with fixups and owner
- S2: expansion over the template substitution engine; `grid` and `zip`; deterministic order; label pattern; collision check; `MaxComposeSlots` bound
- S3: predict reports `axes`, `mode`, `expanded_count` and refuses over-limit sweeps; parity test against the runtime refusal

### E2 — Every surface accepts a sweep, and the MMM grid is one request
- S1: library, CLI (`--parallel`, `--stream`, `--echo-request`) and MCP paths covered by tests; feature-profile capability row (`Since`, edge on `capability:compose`); a profile without it refuses the slot as never-registered
- S2: skills, examples-library entry, docs page, CLAUDE.md Compose-envelope note, `update-demand.md` row
- S3: acceptance test over the MMM fixture (or a hermetic cohort built with the same columns): the sweep reproduces `req/06_decay_grid.json` slot for slot, bit-identical

### E3 — Rank the finished slots
- S1: `rank` path grammar and resolution; `PULSE_SWEEP_RANK_PATH`
- S2: `ComposedResponse.Ranking`; `top`; payload-schema golden; ties broken by expansion order

## Acceptance criteria

- [ ] The fixture-README sweep produces 256 slots labelled `tv<λ>_se<λ>_so<λ>_di<λ>`, each with a `residual_std_err` bit-identical to the same label in `fixtures/mmm-harbor-pine/req/06_decay_grid.json`
- [ ] Its ranking (`asc`, `top: 3`) is `tv70_se10_so30_di70` (14.004354530446564), `tv70_se10_so50_di70`, `tv70_se30_so50_di70`
- [ ] A 7 × 4 × 4 × 4 = 448-slot sweep runs under the default limits; a sweep expanding past `MaxComposeSlots` is refused by predict and runtime with `PULSE_LIMIT_EXCEEDED` before any slot runs
- [ ] A compose request without `sweep` is byte-identical on the wire and in `CanonicalHash` to today
- [ ] `format_version` stays `"1.1"`; every change is additive
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestManifestErrorCodesComplete`
- `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`, `TestTemplatePackage_ImportBoundary`
- `TestComposedResponse_OverlayFreeByteIdentical` stays green (sweep-free and ranking-free responses unchanged)
- New: expansion-order and label tests, the predict/runtime limit parity test, the MMM acceptance test. Avoid the prefix-matched gate families unless the gate is also listed in CLAUDE.md

## Update Demand companions

- `update-demand.md`: a per-slot row for `ComposedRequest.Sweep` and `ComposedResponse.Ranking`
- CLAUDE.md "Output Format Contract" (Compose envelope) + `docs/src/contract/payload-schema.md` + regenerated `descriptor/testdata/payload-schema.json` (keep CLAUDE.md ≤ 40,000 bytes)
- `errors/fixup_metadata.go` + `internal/descriptor/error_owners.go` for both new codes
- `internal/descriptor/features.go` capability row + `docs/src/library/feature-profiles.md`
- `skills/compose-requests.md`, `skills/tool-compose.md` (its `## When to use` lead stays the tool description's lead), `docs/src/library/tuning-limits.md`
- `request-templating.md` only if the template package's exported surface changes

## Human inputs & decisions

- **Open:** may `sweep` be combined with explicit `requests` (sweep slots appended after them), or are the two mutually exclusive? Combining lets a baseline sit beside the grid for a Compose overlay.
- **Open:** can Compose overlays address sweep slots by label (e.g. `OVERLAY_DELTA_VS_REF` of every sweep slot against one explicit baseline)? Falls out naturally if the two are combinable.
- **Open:** the rank-path grammar. It becomes a stable public contract; consider reusing the `Request.Return` path vocabulary rather than inventing one.
- **Open:** with `rank.top`, are the non-top slot responses dropped from `responses` (smaller payload, but `responses[i]` no longer answers expanded slot `i`) or kept (only `ranking` is trimmed)?
- **Decided:** U40 ships in v1.0.0, so it is in U32's `depends_on` and the docs audit covers it. U41 to U43 build on it and carry their own v1 membership call.

## Handed on

Everything the MMM worked example needed outside Pulse and this unit does not deliver. Each row has a TODO id and an owner. Gaps 1, 2, 12 and 13 are partly or wholly this unit's; their remainder is in the rows. The gap numbers refer to the maintainer's gap list for the worked example.

| Gap | What is still outside Pulse | TODO | Owner |
|---|---|---|---|
| 3 | `WIN_EWMA` takes `alpha`; the labels carry the decay, so `alpha = 1 - decay` is hand arithmetic | #284 | [U41](U41-derived-cohort.md) |
| 4 | Materialising derived columns into a new cohort (stream to NDJSON, then import with a hand-written schema) | #280 | [U41](U41-derived-cohort.md) |
| 5 | NDJSON import maps schema fields to keys by position, not by name | #279 | [U41](U41-derived-cohort.md) |
| 6 | Streaming with projection into an import silently drops columns | #281 | [U41](U41-derived-cohort.md) |
| 20 | Inferred `f32` drifts on money-like sums | #282 | [U41](U41-derived-cohort.md) |
| 1 (windows) | The 23-entry window list in the adstock request is generated by a script | #283 | [U41](U41-derived-cohort.md) |
| 7 | Fit coefficients are copied into later `ATTR_FORMULA` strings | #286 | [U42](U42-fit-scoring.md) |
| 8 | Holdout MAPE is a hand-expanded formula; no error aggregator | #287 | [U42](U42-fit-scoring.md) |
| 9 | Coefficient interval bounds are computed outside and pasted in | #288 | [U42](U42-fit-scoring.md) |
| 12 | Scenario slots restate the whole model (the sweep fans the slots out; applying a named fit is U42) | #286 | [U42](U42-fit-scoring.md) |
| 10 | ROI and marginal ROI are divided by the reader | #290 | [U43](U43-post-aggregation-ratios.md) |
| 11 | Scenario scale factors are computed by hand from channel totals | #291 | [U43](U43-post-aggregation-ratios.md) |
| 14 | A later request names the winning axis values by hand | #297 | U40 follow-up |
| 15 | The decay identifiability reading is an eyeball of the grid | #298 | U40 follow-up |
| 16 | Ridge cannot leave a predictor subset unpenalised | #293 | [U36](U36-reference-oracles.md) |
| 17 | `WIN_LAG` default 0 biases lag-1 autocorrelation; post-tests cannot skip null | #294 | [U36](U36-reference-oracles.md) |
| 18 | OLS standard errors ignore cross-group correlation; no clustered or HAC option | #295 | [U25](U25-multivariate-tests-segmentation.md) |
| 21 | Cross-check tables assembled from per-group matrices by hand | #296 | [U28](U28-matrix-overlays.md) |
| 19 | The example's data generator is external | none | Out of scope; the fixture README says so |
