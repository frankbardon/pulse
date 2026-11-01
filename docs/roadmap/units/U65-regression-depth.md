---
id: U65
slug: regression-depth
title: "A fit can be diagnosed, interpreted and compared the way an analyst does it in Stata or SPSS"
track: Statistical integrity
size: L
status: in-progress
depends_on: [U64]
soft_depends_on: [U53, U57]
blocks: []
todo_items: [598, 599, 600, 601, 602, 603, 604, 605, 606, 607]
branch: regression-depth
---

# U65 — regression-depth

**Outcome:** A fit can be diagnosed, interpreted and compared the way an analyst does it in Stata or SPSS.

**Track:** Statistical integrity · **Size:** L · **Depends on:** [U64](U64-reference-validation.md) (E1 registry gate) · **Soft:** [U53](U53-custom-regressions.md), [U57](U57-transformed-predictors.md)

## Summary

Regression is the widest gap against Stata (`regress`, `glm`, `predict`, `margins`, `estat`) and SPSS (REGRESSION, GENLIN, PLUM, NOMREG). It also underpins media-mix and driver analysis, Pulse's niche.

**Already there (inventory 2026-10-31):**
- **Models:** `REG_OLS`, `REG_GLM` (binomial-logit, poisson-log and gamma-inverse only), `REG_BAYES_LINEAR`.
- **Covariance:** `RegressionSpec.Covariance` offers classical, HC0–HC3, cluster (CR1) and HAC. `Vcov` returns the vcov matrix and its correlation.
- **Fitting options:** weights, penalties, selection, jackknife and resampling.
- **Per-row attributes:** `ATTR_REG_FITTED` and `ATTR_REG_RESIDUAL` (OLS only), `ATTR_REG_LEVERAGE` (unpenalized OLS) and `ATTR_REG_PREDICT` (inverse link included).
- **Collinearity:** VIF only through `MAT_COLLINEARITY`.

**Missing:**
1. Per-row diagnostics for GLM and Bayesian fits.
2. Standardized and studentized residuals, Cook's distance, Pearson and deviance residuals.
3. The non-canonical links, whose names are already accepted but refused: probit, cloglog, identity, sqrt and log.
4. Marginal effects.
5. VIF and nested-model tests on the fit itself.
6. Ordinal and multinomial outcomes.

Robust standard errors are NOT a gap. Studio should surface them (E5).

Every output this unit adds lands with an R golden at 1e-10 and its row in the U64 validation registry. The registry gate fails the PR otherwise.

## References

- `.claude/reference/update-demand.md` (regression slice), `weighting.md`, `response-components.md`, `matrix-and-vectors.md` (Regression vcov), `guided-analysis.md`
- `types/regression.go` (spec, covariance, links), `internal/processing/regression/glm_families.go`, `internal/processing/attribute_reg*.go`
- `docs/src/internals/adding-regression.md`, `docs/src/regression.md`; skills `op-reg-ols`, `op-reg-glm`
- R references: `stats` (`rstandard`, `rstudent`, `cooks.distance`, `hatvalues`, `residuals.glm`, `anova`), `car::vif`, `lmtest::waldtest` / `lrtest`, `marginaleffects`, `MASS::polr`, `nnet::multinom`, `MASS::glm.nb`

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#623** `ATTR_REG_RESIDUAL` `params.kind` over a bound fit (`params.fit`): `response` (default), `pearson`, `deviance` and `working`, as R's `residuals.glm`. Bound GLM and Bayesian-linear fits already scored fitted values and response residuals. GLM leverage moves to #624 with the influence measures, since it needs the fit's (XᵀWX)⁻¹ (E1-S1)
- [x] **#624** Influence diagnostics per row: standardized and studentized residuals, Cook's distance and DFFITS, for OLS and GLM (E1-S2). On the OLS refit (`ATTR_REG_RESIDUAL` kind `standardized` / `studentized`, new `ATTR_REG_INFLUENCE` `measure` `cooks` / `dffits`; statsmodels `OLSInfluence` at 1e-9, frequency weights on the `rep()` expansion) and over a bound `REG_OLS` / `REG_GLM` fit (`params.fit` on the residual kinds, `ATTR_REG_LEVERAGE` and `ATTR_REG_INFLUENCE`; GLM leverage from the fit's `vcov` and new `RegressionResult.dispersion`, model file version 4; R 4.6.1 `hatvalues` / `rstandard` / `rstudent` / `cooks.distance` / `dffits` at 1e-9, `glm_influence.json`). Follow-up: weighted bound fits are refused (`requires: unweighted`) — the prior-weighted form is not built yet
- [x] **#625** The non-canonical GLM links already named in the spec are implemented, not refused: binomial probit and cloglog, poisson identity and sqrt, gamma log and identity (E2-S1). R's `make.link` functions, one shared (family, link) rule for predict, the engine and the manifest (`internal/reglink`; the link param's `enum_values_by`), R 4.6.1 `glm` + `sandwich` 3.1-3 at 1e-9 (`glm_links.json`)
- [x] **#626** Negative binomial family (NB2, theta estimated) for over-dispersed counts (E2-S2): `family: negative_binomial` on `log` (default) / `identity` / `sqrt`, θ by maximum likelihood (MASS `glm.nb`) or fixed (`theta`), `theta` / `theta_std_err` / `two_log_lik` / `aic` on the result, model file version 5; R 4.6.1 + MASS 7.3-66 + `sandwich` 3.1-3 at 1e-9 (`glm_nb.json`)
- [x] **#627** Average marginal effects and marginal effects at the mean, with delta-method standard errors that honour the chosen `Covariance` (E3-S1): opt-in `RegressionSpec.Margins` `{type ame | mem, terms, scale response | link}` → `RegressionResult.Margins` on `REG_OLS` / `REG_GLM` (every family and link), delta method on the fit's own vcov (classical, HC, cluster, HAC), the coefficient table's t / z, weighted means, the regression's multiplicity family; R 4.6.1 analytic closed forms at 1e-9 and `marginaleffects` 1.0.0 at 1e-7 (`margins.json`)
- [ ] **#628** VIF and tolerance on the regression result, for fits with two or more predictors (E3-S2)
- [ ] **#629** Nested-model comparison between two fits on the same rows: F test (OLS), likelihood-ratio and Wald tests (GLM) (E3-S3)
- [ ] **#630** Ordinal logistic regression (proportional odds), with a Brant-style check of the proportional-odds assumption (E4-S1)
- [ ] **#631** Multinomial logistic regression (E4-S2)
- [ ] **#632** Studio: regression editors and diagnostics charts for all of the above (pulse-studio, E5)

## Decisions (owner, open)

- **D1 — new operators or GLM families for ordinal and multinomial (E4).** Their results are vector-valued: thresholds, and one coefficient set per category. Recommended: new operators `REG_ORDINAL` and `REG_MULTINOMIAL`. That keeps `RegressionResult`'s GLM shape unchanged and gives each its own atomic skill, Purpose and Interpretation.
- **D2 — negative binomial (#626).** Is it in scope for this unit or a follow-up? Recommended: in scope. Over-dispersed counts are common in the marketing and ops data Pulse targets.
- **D3 — marginal-effects surface.** The options are an opt-in `RegressionSpec.Margins` block that adds `RegressionResult.Margins` (additive, `omitempty`), or a post-fit operator over a bound fit. Recommended: the opt-in spec block, mirroring `Vcov`.
- **D4 — v1 timing.** Everything is additive and `format_version` stays `"1.1"`, but new public slots land before the U02 API freeze golden is final. Recommended: E1–E3 before v1.0.0, and E4 in v1.1 unless the owner pulls it in.

**Decided 2026-11-01** (the owner delegated these choices):

- **D1 → new operators** `REG_ORDINAL` and `REG_MULTINOMIAL`.
- **D2 → negative binomial is in scope** (#626).
- **D3 → an opt-in `RegressionSpec.Margins` block**, mirroring `Vcov`.
- **D4 → E1–E3 before v1.0.0; E4 in v1.1** unless the owner pulls it in.
- **Reference oracle while R 4.6.1 is unavailable in the working environment.** The goldens come from a pinned PEP 723 `uv` script with numpy 2.3.3, scipy 1.16.2 and statsmodels 0.14.5, the versions the weighted-reference harness pins. Each script also carries the R calls that reproduce it. An R cross-check through `make reference` is added when R is available, and it never replaces the statsmodels pin silently.

## Configurability (owner rule, 2026-10-31)

An operator exposes a parameter wherever there is a real methodological choice, and its default is the current behaviour. Every story in this unit records its options and defaults. A pure accuracy fix with no result a user would choose differently (for example the origin shift in the mean tests) is not a knob. A contract between operators (byte parity) is not a per-request knob either.
- In this unit: the GLM `link` and residual `kind`, the influence measures, the marginal-effects `type` (`ame` / `mem`) and the comparison `test`, each with a stated default.

## Scope

**In scope**
- GLM and Bayesian per-row diagnostics, influence measures, the non-canonical links, negative binomial (pending D2), marginal effects, VIF on the fit, nested-model tests, ordinal and multinomial logit
- Each output weight-aware, or explicitly refusing a weight, and multiplicity-aware where it emits p-values
- Studio editors and charts

**Out of scope**
- Mixed and multilevel models, panel fixed and random effects, survival models, instrumental variables, quantile regression (later units; see the Studio comparison notes)
- Complex-sample design variance, meaning strata and PSU (the survey-design track, a separate unit)

## Epics & stories

- **E1 — Diagnose a fit, row by row**
  - S1: GLM and `REG_BAYES_LINEAR` paths for the three per-row attributes:
    - GLM residuals selected by `params.kind` (`response`, `pearson`, `deviance`, `working`);
    - GLM hat values from the IRLS weights.

    Goldens from `fitted()`, `residuals(type =)` and `hatvalues()` on `glm`. Penalized fits keep refusing, with their existing code.
  - S2: new attributes, or a `kind` on `ATTR_REG_RESIDUAL` (decided in the story), for standardized and studentized residuals, Cook's distance and DFFITS. Goldens from `rstandard`, `rstudent`, `cooks.distance` and `dffits`. Under frequency weights, the weighted-reference `rep()` expansion pins them too. Interpretation bands cite a registered convention (Cook's D > 4/n is a heuristic, not a convention, so it goes in Caveats).
  - **S1 progress (2026-11-01).**
    - **What the inventory missed.** Bound GLM and Bayesian fits already scored fitted values and response residuals through `params.fit`; "OLS only" described the refit path. So S1 ships `params.kind` on `ATTR_REG_RESIDUAL`.
    - **How it is checked.** It is refused through the shared `model.CheckFitReference`, at predict and at run alike. `TestGLMResidualKinds_MatchStatsmodels` pins it against statsmodels on binomial-logit, poisson-log and gamma-inverse fits (every row, every kind, 1e-8). The golden is `testdata/statsmodels/glm_residuals.json`, regenerated by `make reference-statsmodels`.
    - **Moved to S2.** GLM and Bayesian leverage, and the influence measures.
- **E2 — Links and families**
  - S1: implement the refused links in `glm_families.go`, including variance and starting values for each and the IRLS step-halving that non-canonical links need. Goldens from `glm(family = binomial(link = "probit"))` and the rest. `PULSE_*` refusal codes that no longer fire are removed with their `codeMetadata`.
  - **S1 progress (2026-11-01).**
    - **Links.** The six links are R's `make.link` (probit and cloglog clamp μ to [ε, 1 − ε] and floor dμ/dη at ε; the identity / sqrt links keep μ > 0 through `ValidEta`, so the step halves as `glm.fit`'s does). Defaults stay canonical: an omitted link resolves to the canonical family value itself, so existing requests are byte-identical (`TestGLMLinks_CanonicalDefaultByteIdentical`; the fingerprint golden pins the canonical bits).
    - **The (family, link) rule** is one leaf, `internal/reglink`, read by the engine (`selectFamily`, `validateGLMSpec`), predict (which judged no link before: a parity gap closed, `TestGLMLinks_PredictMatchesRuntime`) and the manifest: the link param gains `enum_by: family` + `enum_values_by` (a new optional `descriptor.Param` pair) so Studio can offer only valid pairs; the MCP `link` key is an enum. The reserved-link branches are gone; `PROCESSING_REGRESSION_INVALID_LINK` stays for another family's link, `details.allowed` the family's three, canonical first (`supported` dropped).
    - **Canonical-link assumptions audited.** The IRLS working weight / response, the classical vcov (expected information, R's `summary.glm`), the HC / cluster / HAC sandwich (score μ′(y − μ)/V, bread (XᵀWX)⁻¹), the bound-fit leverage and influence kernel (DFFITS sign = sign g′(μ)), the residual kinds (working (y − μ)/μ′), deviance / null deviance (μ = ȳ under any link) / AIC, scoring through the inverse link, penalties (Fisher scoring on the penalised objective: KKT-checked), selection, resample and model files (the `link` key already existed, so `model_format_version` does not move) were already link-general. The one change in the loop: a non-canonical link's first step that lands invalid (R's glm stops, "no valid set of coefficients") is halved toward the intercept-only fit; the canonical links keep their path.
    - **Oracle.** `scripts/reference/gen_glm_links.R` (R 4.6.1, `sandwich` 3.1-3 installed from the Posit binary repo): six unweighted cases plus a frequency-weighted one per family against the `rep()` expansion; glm refitted to its fixed point (glm's deviance test stops ~1e-9 off the MLE under Fisher scoring's linear convergence). Max error at Pulse tol 1e-13: 3e-13 (coefficients), 1e-14 (SEs, HC SEs), 3e-14 (vcov), 0–4e-16 (deviances, AIC), 6e-12 (working residuals near 0). At the default tol 1e-8 the coefficients sit within 3e-8 of the MLE (documented; tightening the default is an owner choice).
    - **No refusals added**: no path is wrong under a non-canonical link. Penalized non-canonical fits are checked by KKT only (glmnet is not in the pinned toolchain here).
  - S2 (D2): NB2 with theta estimated by ML, alternating with IRLS. Goldens from `MASS::glm.nb`.
  - **S2 progress (2026-11-01).**
    - **Family.** `family: "negative_binomial"` (`internal/reglink.NegativeBinomial`), links `log` (MASS's default), `identity`, `sqrt` registered in the shared (family, link) table, so predict, the engine, the manifest's `enum_values_by` and the MCP enum gain them together. V(μ) = μ + μ²/θ, the unit deviance and μ⁰ = y + (y = 0)/6 are MASS's `negative.binomial`; the link functions are poisson's.
    - **Options and defaults (owner rule).** θ estimated by maximum likelihood is the default; `theta` (> 0) fixes it — R's `glm(family = negative.binomial(theta))`. Everything else (other families, existing requests) is byte-identical; the fingerprint golden did not move.
    - **Estimation.** A poisson fit on the same link, then rounds of θ ← Newton on the θ score at the fitted means (`theta.ml`: moment start, then the previous θ; relative step 1e-12, a round-off stall accepted below 1e-9) and β ← IRLS warm-started at that θ, until a round moves θ by ≤ `tol` relative and β by < `tol` (the IRLS rule), then one IRLS fit at θ̂ so β, deviances and `vcov` are at θ̂. Counts with no over-dispersion (θ̂ → ∞) are `PROCESSING_REGRESSION_NO_CONVERGE` `reason: theta_unbounded`. `digamma` / `trigamma` are portable series (recurrence to x ≥ 12, asymptotic expansion) on `detmath.Log`; lgamma is `detmath.Lgamma`; every product is barriered.
    - **Outputs.** `theta`, `theta_std_err` (1/√I(θ̂), estimated only), `two_log_lik`, `aic` (θ counted when estimated, MASS's; not when fixed, `AIC(glm)`); `deviance` / `null_deviance` at θ̂; `vcov` / SEs (XᵀWX)⁻¹ at θ̂, φ = 1, Wald z (`summary.negbin`, also for a fixed θ — R's `summary.glm` would estimate a dispersion there). Model files carry θ: `model_format_version` 5 (a new field is a version bump).
    - **Downstream.** Correct: HC / cluster / HAC (θ held at θ̂, as `sandwich` on `glm.nb`; HC0 / HC1 / HC3 oracled), the residual kinds (V and the deviance at θ̂), leverage, influence (`BoundInfluence`, φ = 1; R's `rstudent` / `dffits` on a bare negbin read the leave-one-out σ because `stats:::estDisp` is TRUE for a family with no dispersion slot — the golden takes them at `family$dispersion <- 1`), `ATTR_REG_PREDICT` and bound-fit scoring (link only), frequency and probability weights (prior weights; frequency = the `rep()` expansion, oracled; constant probability weights = unweighted). Refused with `PROCESSING_CONFIG` and `details.requires`, the same at predict and run (`internal/reglink.CheckTheta`): `penalty` (`unpenalized`), `selection` (`no_selection`), `resample` (`no_resample`) — their refit drivers hold θ fixed — and `theta` off the family or ≤ 0. Weighted bound-fit influence keeps its existing refusal.
    - **Fixed-θ dispersion (owner-delegated follow-up).** `dispersion` `fixed` (default, φ = 1) or `estimated` (φ̂ = Pearson χ²/df, R's `summary(glm(family = negative.binomial(θ)))`) on a fixed-θ fit: vcov, t inference, residual standardization and bound-fit influence (studentized / DFFITS on the leave-one-out σ, R's `rstudent` / `dffits` on that fit) follow φ̂; its model file is `model_format_version` 6 (θ + φ̂). Refused, coded with `requires` and identical at predict and run (`reglink.CheckTheta`): another family or regression, θ estimated, another value. Three new oracle cases (log, sqrt-with-influence, frequency vs `rep()`): max relative error 5.5e-13 (p-values), 1.4e-13 (intervals), 1.5e-14 (φ̂), 1.7e-14 (vcov), 5.4e-13 (Cook's), 3.6e-13 (studentized / DFFITS), 4.4e-12 (deviance residuals near 0).
    - **Log-likelihood and AIC on every family (owner-delegated follow-up).** `two_log_lik` / `aic` on every plain `REG_OLS` (`logLik.lm`, σ counted) and `REG_GLM` fit (binomial / poisson / gamma by the family's `aic`, gamma's φ = D/n counted; the negative binomial unchanged), R's `logLik()` / `AIC()` exactly; frequency = the `rep()` expansion; absent under probability weights (a pseudo-likelihood — the negative binomial's probability-weighted fit drops its two, flagged to the owner), penalties, a binding OLS bound and `REG_BAYES_LINEAR`. Selection's gamma and OLS criteria share the likelihood code (`gammaLogLik`, `gaussianLogLik`); binomial / poisson selection keeps deviance + 2k (a constant off `aic` on shared rows). Oracle `gen_loglik.R` (9 cases) at 1e-10: max relative error 6.1e-16 (2ℓ), 5.7e-16 (AIC); the links' AIC against `glm_links.json` 1.7e-16; the fingerprint golden did not move.
    - **Oracle.** `scripts/reference/gen_glm_nb.R` (R 4.6.1, MASS 7.3-66 installed into the user library — this R build ships no recommended packages — `sandwich` 3.1-3): log / sqrt / identity with θ estimated, θ fixed at 1.7, a frequency-weighted case against the `rep()` expansion and an influence case; glm.nb pushed to its joint fixed point (its own stop sits ~1e-6 off). Max relative error at Pulse tol 1e-13: coefficients 3.7e-13, SEs 9.5e-15, vcov 2.3e-14, θ 7.6e-14, SE(θ) 1.5e-13, 2ℓ 5.4e-16, AIC 5.1e-16, deviances 1.9e-14, HC SEs 4.0e-14, fitted 2e-14, Pearson / working residuals 3.1e-13, deviance residuals 6.0e-12 (near-zero values), hat 8.7e-15, standardized / studentized / DFFITS 3.1e-13, Cook's 9.8e-14. At the default tol θ and β sit within 1e-6 of the MLE.
- **E3 — Interpret and compare fits**
  - S1 (D3): AME and MEM for continuous and factor predictors. Delta-method standard errors use the fit's own vcov, so HC, cluster and HAC carry through. Goldens from pinned `marginaleffects::avg_slopes` / `slopes(newdata = "mean")`.
  - **S1 progress (2026-11-01).**
    - **Surface (D3).** `RegressionSpec.Margins` (`margins`, omitempty) → `RegressionResult.Margins` (omitempty): `{type, scale, terms, estimates, std_errors, statistics, p_values, ci_lower, ci_upper, df, conf_level, at, p_adjusted, significant_adjusted}`, maps keyed by term like the coefficient table. Absent is byte-identical (`TestMargins_AbsentIsByteIdentical`); `format_version` stays `"1.1"`. No feature row (a param, like `vcov`).
    - **Options and defaults (owner rule).** `type` `ame` (default) or `mem` (at the weighted predictor means, echoed as `at`); `terms` default every predictor; `scale` `response` (default, dμ/dx) or `link` (dη/dx = the coefficient). On the link scale and on any identity link (all of `REG_OLS`) every figure is the coefficient's, bit for bit.
    - **Factors.** Pulse regressions take numeric predictors only (fieldaccept refuses a categorical's index), so there are no factor terms; the discrete-contrast half of the story does not apply. A 0/1 dummy gets a slope. Derived predictors (feature / attribute / window outputs) are refused (`requires: schema_predictors`): no chain rule through, e.g., FEAT_POLY's x² or an interaction.
    - **Inference.** Delta method on the fit's own vcov (classical φ·(XᵀWX)⁻¹ or the robust sandwich — HC0–HC3, cluster, HAC carry through); the coefficient table's reference distribution (t on `df` for OLS and classical gamma, z otherwise) at the fit's `conf_level`; weighted means (marginaleffects `wts`; frequency = the `rep()` expansion). Under the regression's own `multiplicity` block the terms join its family after the member coefficients (owner call below); predict's p-value count includes them.
    - **Execution modes.** OLS's effects are its coefficients: no row pass, the block streams and merges across shards with the fit (`at` from the accumulator's means). GLM is buffered: one derivative pass over the fitted rows in record order after IRLS converges — the same rows and order the deviance sums — so it is bit-identical across `DecodeWorkers` / `ShardWorkers` (`TestMargins_WorkerInvariant`, OLS streamed ≡ buffered too). Products barriered; the fingerprint golden did not move.
    - **Refused** (`PROCESSING_CONFIG`, `details.requires`, identical at predict and run — `internal/regmargins`, `TestMargins_PredictMatchesRuntime`): penalty (`unpenalized`), selection (`no_selection`), resample (`no_resample` — a replicate vcov is not the AME's; bootstrapping the effect itself is not built), bounds (`unbounded`), `REG_BAYES_LINEAR` and extension / model regressions (`type REG_OLS or REG_GLM`; the shared-slot table's `slot: margins` for the latter), bad `type` / `scale` / `terms`, derived predictors.
    - **Oracle.** `scripts/reference/gen_margins.R` (R 4.6.1, MASS 7.3-66, `sandwich` 3.1-3, `marginaleffects` 1.0.0 + insight 1.7.0 + data.table 1.18.6.1 from the Posit noble binary repo): 17 cases — lm ame / mem, logit ame / mem / link / HC3, probit, cloglog, poisson ame / mem / link / cluster, gamma inverse ame, gamma log mem, negative binomial ame, frequency-weighted logit ame / mem against the `rep()` expansion. Max relative error vs the analytic closed forms 8.8e-13 (a gamma interval bound near 0; estimates 7.9e-15, SEs 2.9e-15, p-values 4.8e-14); vs `marginaleffects` 2.3e-9 (estimates) and 6.8e-9 (SEs), held at 1e-7.
    - **Owner calls taken (flag if wrong).** (a) Margins p-values POOL into the regression's own correction family (m counts coefficients + terms), not a family of their own; (b) a weighted `mem` evaluates at the weighted means (Stata), where `marginaleffects` `newdata = "mean"` takes unweighted ones; (c) derived predictors are refused rather than differentiated as given.
  - S2: `RegressionResult.Collinearity`, with VIF and tolerance per term and generalized VIF for factor terms. Goldens from `car::vif`. It shares the arithmetic with `MAT_COLLINEARITY` rather than duplicating it.
  - S3: a Compose-level comparison of two fits named in `fits[]`. Pulse refuses the comparison unless the fits are nested and on the same rows. It runs an F test for OLS and LR and Wald tests for GLM. Goldens from `anova(m1, m2)`, `lmtest::lrtest` and `waldtest`. The result is multiplicity-aware.
- **E4 — Ordinal and multinomial outcomes** (D1)
  - S1: `REG_ORDINAL` (proportional-odds logit, probit optional), returning thresholds and coefficients, plus a proportional-odds check. Goldens from `MASS::polr`, with the Brant check computed in R from the separate binary fits.
  - S2: `REG_MULTINOMIAL` (baseline-category logit). Goldens from `nnet::multinom`, with tight tolerances only after convergence is forced to its optimum.

  For each operator: atomic skill, capability entry, example tag, `Since`, Purpose and Interpretation, weight handling and registry row.
- **E5 — Studio** (pulse-studio, after each Pulse tag; `make bump`, then `make catalog-fixture` and `make cast-matrix`)
  - S1: rail editors.
    - A Covariance picker (classical, HC0–HC3, cluster with a field picker, HAC with a lag), and family and link enums filtered to valid pairs.
    - Margins and comparison settings, via `ui/src/graph/fieldHints.ts`, so they stop falling back to JSON.
    - The coefficient table names its standard-error type.
  - S2: diagnostics charts.
    - Plots: fitted versus actual, residuals versus fitted, normal QQ of standardized residuals, and leverage versus studentized residual with Cook's contours.
    - Drawn from the per-row attributes, which removes the README's "no fitted-versus-actual chart" gap.
    - Charts sample past `--auto-preview-rows`, like other previews.
  - S3: a marginal-effects table and chart (estimate ± CI per term), a nested-comparison card in Compose results, and ordinal and multinomial result tables.
  - S4: Studio tests (vitest, Playwright) for each editor and chart. The README's Chart gaps and Known limitations are updated.

## Acceptance criteria

- Every new statistic matches its R reference at 1e-10 relative, or a documented looser bound for iterative fits, and has a U64 registry row.
- Every new output is weight-aware or refuses a weight with a coded error, and emits `p_adjusted` under `multiplicity` where it has a p-value.
- `format_version` stays `"1.1"`: every wire change is additive.
- A Studio user can fit a logit with clustered standard errors, read its average marginal effects and inspect influence plots without editing JSON.

## Gates & tests

- `TestSkillsCoverAllRegressions`, `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`, `TestPayloadSchemaGolden`, `TestDocsGeneratedCurrent`
- The U64 validation gates, and `TestGoldensNotHandEdited` for every new R golden
- Studio: `make check` (tsc, eslint, vitest, Playwright), with paramForms drift tests after `make catalog-fixture`

## Update Demand companions

- The regression and attribute rows of `.claude/reference/update-demand.md`: atomic skills `op-reg-*` / `op-attr-*`, `internal/descriptor/capabilities_*.go`, example tags, `make docs`
- `weighting.md` (weight class of each new output), `response-components.md` (any new Components keys), multiplicity row, `guided-analysis.md` (Purpose / Interpretation)
- `docs/src/regression.md`, `docs/src/internals/adding-regression.md`, `regenerating-goldens.md` (new R scripts)
- Studio: README (Rail editors, Charts, Known limitations), `ui/src/graph/fieldHints.ts`, regenerated fixtures

## Handed on to later work

- Mixed models, panel fixed and random effects, time-series models (ARIMA / state space), and complex-sample design variance, each a separate unit.
