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
- [ ] **#625** The non-canonical GLM links already named in the spec are implemented, not refused: binomial probit and cloglog, poisson identity and sqrt, gamma log and identity (E2-S1)
- [ ] **#626** Negative binomial family (NB2, theta estimated) for over-dispersed counts (E2-S2, pending D2)
- [ ] **#627** Average marginal effects and marginal effects at the mean, with delta-method standard errors that honour the chosen `Covariance` (E3-S1)
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
  - S2 (D2): NB2 with theta estimated by ML, alternating with IRLS. Goldens from `MASS::glm.nb`.
- **E3 — Interpret and compare fits**
  - S1 (D3): AME and MEM for continuous and factor predictors. Delta-method standard errors use the fit's own vcov, so HC, cluster and HAC carry through. Goldens from pinned `marginaleffects::avg_slopes` / `slopes(newdata = "mean")`.
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
