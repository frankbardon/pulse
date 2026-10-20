---
id: U53
slug: custom-regressions
title: "Embedders register their own regression models, treated exactly like built-ins, and the core gains the model options media-mix work needs"
track: API & release
size: L
status: not-started
depends_on: []
soft_depends_on: [U34, U36, U42]
blocks: [U32]
todo_items: [381, 382, 383, 384, 385, 386, 387, 388, 389, 293, 401, 454]
branch: custom-regressions
---

# U53 — custom-regressions

**Outcome:** Embedders register their own regression models, treated exactly like built-ins, and the core gains the model options media-mix work needs.

**Track:** API & release · **Size:** L · **Depends on:** none · **Soft:** [U34](U34-extension-validation.md) (registration validation), [U36](U36-reference-oracles.md) (oracle harness), [U42](U42-fit-scoring.md) (`ATTR_REG_PREDICT`) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

`Options.Extensions` has eight registration categories (seven operator kinds plus synth distributions), expression functions and tables, but none for regressions: the three built-ins (`REG_OLS`, `REG_GLM`, `REG_BAYES_LINEAR`) are the whole set, and `types.RegressionSpec` is a fixed struct with no place for an operator's own parameters. A media-mix model needs fits the built-ins do not cover (saturation parameters estimated jointly, partial pooling across regions), and the owner decided a custom regression is a v1 requirement because the public surface freezes at v1.0.0.

Internally the regression registry already separates a streaming engine (update a row, finalize) from a buffered one (fit over all records). This unit exposes that split through the public `extend` package, adds an operator-owned params block and outputs map to the wire (additive), and wires custom regressions through every surface a built-in reaches. It also adds the small model options the MMM review found missing in the core: per-coefficient prior strength, non-negative coefficients, Weibull adstock and a Hill function.

**Source of inspiration:** the [MMM worked example](../fixtures/mmm-harbor-pine/README.md) and a review of ten published media-mix case studies. The design and the layering decision (core, model packs, an optional R bridge, the casebook) are in [api-and-release 04](../v1.0.0-api-and-release/04-model-extensions.md).

Numbering note: appended after U52, not renumbered. **Release intent:** v1.0.0 (owner decision 2026-10-10: custom regressions are a must for v1).

## References

**Read before starting:**
- [api-and-release 04](../v1.0.0-api-and-release/04-model-extensions.md) (the layering decision and what stays out of core)
- `.claude/reference/architecture.md` (Extension surface), `.claude/reference/update-demand.md`, `.claude/reference/guided-analysis.md`, `.claude/reference/weighting.md`, `.claude/reference/feature-profiles.md`, `.claude/reference/response-components.md`
- `docs/src/internals/extension-points.md`, `skills/regression-modeling.md`, `skills/op-reg-bayes-linear.md`, `skills/op-reg-ols.md`
- `internal/processing/regression/registry.go` (`StreamingEngine` / `BufferedEngine`), `extensions.go`, `extend/`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#381** (Custom regressions) `Extensions.Regressions` and public `extend` regression interfaces: a streaming form (row update, merge, finalize) and a buffered form (fit over all rows, may iterate), declared at registration the way `Streamable()` is; names are `REG_<NAMESPACE>_<NAME>`, and reserved namespaces and built-in collisions are refused at `pulse.New`
- [ ] **#382** (Custom regressions) Wire: `RegressionSpec.Params` (raw JSON, decoded strictly against the registration's declared params schema) and an operator outputs map on `RegressionResult` keyed by a declared schema in the manifest; payload-schema golden regenerated; additive, `format_version` stays `"1.1"`
- [ ] **#383** (Custom regressions) Parity: custom regressions reach predict (params validation, streamability), manifest, payload schema, MCP, feature profiles (`Since`, dependency edges), guided analysis (`Purpose` / `Interpretation` hook), weighting (weight-aware or a coded refusal) and multiplicity (p-values join a family); the `TestExtensions_*` parity suites cover the new category
- [ ] **#384** (Custom regressions) Composition: a registration may supply a row scorer so `ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL`, `ATTR_REG_PREDICT` (U42) and compose-sweep ranking work on custom fits; the `resample` and `selection` modifiers work with buffered custom regressions that can refit, and are refused with a coded error otherwise
- [ ] **#385** (Custom regressions) A post-fit extension slot for computations over fitted results (response-curve evaluation, budget allocation under constraints), or a recorded decision to ship it in v1.x (it is additive either way)
- [ ] **#386** (Custom regressions) `REG_BAYES_LINEAR` `prior_precision` accepts a per-coefficient vector (intercept first, as `prior_mu`), the scalar form unchanged; `REG_OLS` takes non-negativity bounds on a named predictor subset; oracle cases against R for both
- [ ] **#387** (Custom regressions) Media-response primitives: Weibull adstock (CDF and PDF kernels, normalised, beside the existing geometric `WIN_EWMA`) as a window option or `WIN_ADSTOCK`, and a `hill(x, half_saturation, shape)` expression function; null handling matches the other windows
- [ ] **#388** (Custom regressions) Recipe and reference extension: `docs/src/internals/adding-regression.md`, and a test-only buffered extension (a Hill nonlinear least-squares fit through `gonum/optimize`) proving iterative fits work through the public contract
- [ ] **#389** (Custom regressions) Acceptance: the MMM fixture fits Hill saturation through a registered custom regression, with non-negative media coefficients and an experiment-centred prior on one channel, and every step stays pure Pulse plus that registration

## Scope

**In scope**
- The regression extension category, end to end
- An operator-owned params block and outputs map on the regression wire
- A post-fit extension slot (or the decision to defer it)
- Per-coefficient prior precision, non-negative OLS coefficients
- Weibull adstock and a Hill expression function
- The recipe and a reference extension
- The three model-surface items U36 handed on (see "Handed on from U36"): ridge over a predictor subset (#293), panel-robust covariance (#401) and the exact McNemar test (#454), each with its R oracle case

**Out of scope** (see [api-and-release 04](../v1.0.0-api-and-release/04-model-extensions.md), "After v1.0.0")
- Shipped model packs (MMM pack: Hill NLS, partial pooling, budget allocator)
- An R bridge, in any form, in core
- The `pulse-casebook` repository and the move of the showcase examples (already moved to it) and the MMM fixture
- MCMC or any sampler in core

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(custom-regressions/E<n>-S<m>): …`; close each epic with `milestone(custom-regressions/E<n>): vertical slice complete — <epic title>`.

### E1 — A registered regression runs like a built-in
- S1: `extend` streaming and buffered regression interfaces; `Extensions.Regressions`; name and collision validation
- S2: `RegressionSpec.Params` and the outputs map; strict params decode; payload schema
- S3: predict, manifest, MCP, feature profiles, guidance, weighting and multiplicity parity; parity suites

### E2 — Custom fits compose
- S1: row scorer; fitted / residual / predict attributes and sweep ranking on custom fits
- S2: modifiers on buffered custom regressions, or the coded refusal
- S3: post-fit slot (or the recorded deferral)

### E3 — Media-response options in core
- S1: per-coefficient prior precision; non-negative OLS coefficients; oracle cases
- S2: Weibull adstock; `hill()`
- S3: recipe, reference Hill NLS extension, MMM acceptance

## Acceptance criteria

- [ ] A test embedder registers a streaming and a buffered custom regression; both appear in `pulse manifest --json`, `pulse schema`, `pulse_predict` and MCP exactly as a built-in does, and a feature profile can hide them
- [ ] Unknown keys in a custom regression's `params` are refused at predict and at runtime with the same code
- [ ] `ATTR_REG_RESIDUAL` and a compose sweep ranked by `residual_std_err` work on a custom fit
- [ ] Per-coefficient prior precision and non-negative OLS match R within oracle tolerance
- [ ] The MMM fixture fits Hill saturation with no transcribed number
- [ ] `format_version` stays `"1.1"`; every change is additive; `TestPublicAPIGolden` is regenerated with `-update`, never hand-edited
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPublicAPIGolden`, `TestPayloadSchemaGolden`, `TestExtendImportBoundary`, `TestRootSurfaceNamesNoProcessing`, `TestExtensions_*`, `TestSkillsCoverAllRegressions`, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes`, `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`, the regression reference oracles
- New: extension-regression parity (predict vs runtime, streaming vs buffered), strict params decode, Weibull adstock and `hill()` oracles
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- CLAUDE.md: "Embedder-extensible" and "Extension Points" (the category count and the regression slot); keep it ≤ 40,000 bytes
- `.claude/reference/architecture.md` (Extension surface), `docs/src/internals/extension-points.md`, new `docs/src/internals/adding-regression.md`
- `skills/regression-modeling.md`, `skills/op-reg-bayes-linear.md`, `skills/op-reg-ols.md`, an atomic skill for any new window, `capabilities_*.go`, example tags, `Purpose` / `Interpretation`, `features.go`, weight class in `weighting.md`, `make docs`
- `docs/src/contract/payload-schema.md` and the payload-schema golden for the wire additions

## Handed on from U36

Filed as TODO items (ids are TODO ids, not GitHub issues); U36 closed without them, none is a defect in its oracles.

| Item | Source |
|---|---|
| #293 `REG_OLS` `penalty` with an `unpenalized: [..]` predictor list (ridge leaves a subset, e.g. categorical dummies, unshrunk); add an oracle case against `glmnet`'s `penalty.factor` | [U36](U36-reference-oracles.md), originally [U40](U40-compose-sweep.md) |
| #401 Panel-robust `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` covariance: HAC across a panel (per-entity Newey–West or Driscoll–Kraay) and two-way clustering; `covariance.type: hac` refuses a repeated `order_by` (`order_by_ties`) | [U36](U36-reference-oracles.md), originally [U25](U25-multivariate-tests-segmentation.md) |
| #454 `OVERLAY_SYMMETRY` exact binomial McNemar (`params.exact`, 2 × 2) and the discordant odds ratio as an effect size, both pinned to R (`binom.test`) | [U36](U36-reference-oracles.md), originally [U30](U30-matrix-extensions-hardening.md) (owner decision 2026-10-14) |

## Human inputs & decisions

- **Decided (2026-10-10):** custom regressions ship in v1; no R in core; model packs and an R bridge are separate modules built on this contract; the casebook is a separate repository created after the example-converting units land.
- **Open:** the post-fit slot ships in v1 or v1.x (#385).
- **Open:** Weibull adstock as a `WIN_EWMA`-family option or a new `WIN_ADSTOCK` operator (#387).
- **Open:** the outputs map is free-form under a declared schema, or reuses the Components `Operator` map convention (#382).
