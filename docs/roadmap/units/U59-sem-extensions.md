---
id: U59
slug: sem-extensions
title: "MAT_SEM compares latent means across groups under invariance constraints, puts intervals on derived effects, scores latents per row and fits ordinal ratings"
track: Vector & matrix
size: L
status: done
depends_on: []
soft_depends_on: [U24, U40, U42]
blocks: [U32]
todo_items: [528, 529, 530, 531, 532, 533, 534, 535, 536, 537, 538, 589]
branch: sem-extensions
---

# U59 — sem-extensions

**Outcome:** MAT_SEM compares latent means across groups under invariance constraints, puts intervals on derived effects, scores latents per row and fits ordinal ratings.

**Track:** Vector & matrix · **Size:** L · **Depends on:** — · **Soft:** [U24](U24-matrix-operators.md), [U40](U40-compose-sweep.md), [U42](U42-fit-scoring.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`MAT_SEM` fitted a lavaan / semopy model by maximum likelihood to the members' covariance only: no mean structure, no multi-group fit, no defined parameters, no bootstrap, no per-row scores and no ordinal estimator. An analyst who compares latent means across survey waves, tests measurement invariance, puts an interval on an indirect effect, scores respondents or treats 1–5 ratings as ordinal had to fall back on heuristics.

This unit extends `MAT_SEM` in four slices, each validated against lavaan (0.7.3; ≈1e-4 relative on estimates and SEs, ≈1e-3 on fit indices). The legacy single-group covariance-only fit stays semopy-parity and byte-identical.

- **M1 (E1, done):** `meanstructure` and `~ 1` intercepts / latent means; a joint multi-group fit over `params.group` with lavaan's `group_equal` / `group_partial`, the `c(...)*x` per-group modifiers, `NA*x` and lavaan's identification. A fit that uses any of them follows lavaan's conventions.
- **M3 (E2, done):** `:=` defined parameters with delta-method SEs, a seeded bootstrap with percentile intervals, and `ci_level` on every lavaan-convention interval.
- **M2 (E3, done):** `ATTR_SEM_SCORE`, the compose `sem` fit kind and saved `*.pulse-sem.json` fits.
- **M4 (E4, done):** DWLS / WLSMV with polychoric and polyserial correlations for ordered indicators, ordinal multi-group and bootstrap, and EBM scores.

Status: merged in PR #384 (9241affb); shipped in `v1.0.0-alpha.26` (pre-release, with U58).

## References

- `.claude/reference/matrix-and-vectors.md` (slices `matrix-sem.md`, `matrix-sem-fixtures.md`, `sem-scores.md`, `sem-ordinal.md`), `update-demand.md` (vectors-and-matrices slice), `feature-profiles.md`, `guided-analysis.md`
- `internal/skills/op-mat-sem.md`, `op-attr-sem-score.md`, `sem-modeling.md`, `sem-defined-parameters.md`, `sem-bootstrap.md`, `sem-fits.md`, `sem-ordinal.md` (ML vs WLSMV — when to prefer which), `compose-fits.md`, `vector-row-scores.md`, `multiplicity-correction.md`, `matrix-results.md`, `multivariate-design.md`
- `docs/src/library/matrices.md` (Structural equation models: invariance ladder, defined parameters, bootstrap, latent scores, ordinal items, ML or WLSMV?)
- Examples: `internal/examples/matrices/16_sem_multigroup.json`, `internal/examples/compose/09_sem_invariance_sweep.json`, `internal/examples/matrices/17_sem_contribution.json`, `internal/examples/attributes/25_sem_score.json`, `internal/examples/compose/10_sem_fit_scores.json`, `internal/examples/matrices/18_sem_ordinal.json`, `internal/examples/attributes/26_sem_ordinal_score.json`, all on the `brand_survey.pulse` fixture (`internal/brandfixture`, the lavaan oracles' rows, with a planted wave-2 latent-mean shift)
- [Embedder migration: Changes from U59](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#528** single-group mean structure: `meanstructure`, `~ 1`, ν / α, the lavaan-convention switch
- [x] **#529** joint multi-group fit: `group`, `group_reference`, per-level co-moments, the joint result, refusals
- [x] **#530** invariance: `group_equal`, `group_partial`, `c(...)*x` / `NA*x`, lavaan identification
- [x] **#531** predict, manifest and feature-profile parity (`capability:sem_meanstructure`, `capability:sem_groups`)
- [x] **#532** skills, book page, examples and the invariance-ladder recipe
- [x] **#533** `:=` defined parameters with delta-method SEs, joining the multiplicity family
- [x] **#534** seeded bootstrap (`resample: "bootstrap"`), percentile CIs, buffered, weight refused; the derived seed and `ci_level` on the Wald interval
- [x] **#535** `ATTR_SEM_SCORE` (regression / Bartlett), inline `fit_on` or a bound `fit`
- [x] **#536** the compose `sem` fit kind and `*.pulse-sem.json` save / load
- [x] **#537** DWLS / WLSMV with thresholds, polychoric and polyserial correlations, ordinal multi-group and bootstrap
- [x] **#538** EBM scores of ordinal SEM fits, inline / bound / loaded
- [x] **#473** (U32-owned, delivered here) the `weighting/inference-multivariate-and-overlays.md` slice split (E3-S4)

## Scope

**In scope**
- Mean structure, multi-group and invariance on `MAT_SEM` (M1)
- `:=` defined parameters and the bootstrap (M3)
- SEM factor scores, the `sem` fit kind and saved fits (M2)
- The ordinal DWLS / WLSMV estimator (M4)

**Out of scope**
- Bayesian SEM, FIML / pairwise missing data, lavaan `==` / `<` / `>` constraints, `.robust` fit indices, other estimators (ULS, WLSM, MLR …), the theta parameterization, modification indices
- An automatic invariance ladder or a χ² difference / LRT helper (the ladder is a recipe: one slot per step or a compose sweep)
- Request `groups` combined with `params.group` on a SEM slot (refused)
- A new MCP tool, CLI leaf or environment variable

## Epics & stories

### E1 — Analyst compares latent means across groups under invariance constraints (done)
- S1 single-group mean structure, S2 joint multi-group fit, S3 invariance constraints and latent mean differences, S4 predict / manifest / profile parity, S5 docs, skills, examples and this unit
### E2 — Analyst gets intervals on derived effects (done)
- S1 defined parameters with delta-method SEs, S2 seeded bootstrap, S3 contract, docs and example
### E3 — Analyst scores latents per row and reuses SEM fits (done)
- S1 `ATTR_SEM_SCORE` from an inline fit, S2 the compose `sem` fit kind, S3 save and load, S4 contract, docs and example
### E4 — Analyst fits ordinal ratings with WLSMV (done)
- S1 thresholds, polychoric and polyserial correlations, S2 DWLS / WLSMV single group, S3 ordinal multi-group and bootstrap, S4 EBM scores, S5 contract, docs and release notes

## Acceptance criteria

- [x] ML mean structure and the configural / metric / scalar / partial multi-group fits match lavaan within tolerance; the scalar fit on the brand-survey fixture recovers the wave-2 latent mean shifts within 2 SE
- [x] Legacy single-group covariance-only output is byte-identical (semopy parity unchanged)
- [x] The invariance ladder reads from fit indices across fits; a compose sweep over `group_equal` runs it (`compose/09_sem_invariance_sweep.json`)
- [x] `:=` contributions get delta-method or bootstrap intervals and p (`matrices/17_sem_contribution.json`, `TestExamples_SEMContributionRecipe`)
- [x] SEM latent scores are usable downstream; scoring a saved fit is bit-equal to scoring the in-compose fit (`compose/10_sem_fit_scores.json`, `TestExamples_SEMFitScoresFeedRegression`, `TestSEMFit_SaveThenLoadScoresBitEqual`)
- [x] DWLS / WLSMV matches lavaan (thresholds, polychorics, polyserials, Gamma, estimates, robust SEs, scaled-shifted test, `_scaled` indices; single and multi-group), and the docs say when to prefer it over ML (`sem-ordinal` "ML vs WLSMV — when to prefer which", the book's "ML or WLSMV?", `matrices/18_sem_ordinal.json`, `TestExamples_SEMOrdinalRecipe`)
- [x] EBM scores of ordinal fits match lavaan (`TestSEMScore_EBMMatchesLavaan`, `attributes/26_sem_ordinal_score.json`, `TestExamples_SEMOrdinalScores`)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)) — the full gate set and CI run at the PR; `status` flips to `done` at merge

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestManifest*Complete`, `TestSkillTokenBudget`, `TestSkillsCoverFeatureFences`, `TestSkillParamsMatchManifest`, `TestEveryOperatorHasAnExampleTag`, `TestDocsGeneratedCurrent`, `TestInterpretationCoversOutputs`, `TestGuidanceProseLint`
- M1: `TestSEM_MeanStructureMatchesLavaan`, `TestSEM_MultiGroupMatchesLavaan`, `TestSEM_ScalarLatentMeansRecoverTruth`, `TestSEM_LegacyConventionsUnchanged`, `TestSEM_InvarianceComposeSweep`, `TestSEM_HiddenSurfaces*`, `TestSEM_PredictGroupLevels`, `TestExamples_SEMInvarianceLadderSweep` (now on `brand_survey.pulse`: the scalar step recovers the planted shift)
- M3: `TestSEM_DefinedMatchesLavaan`, `TestSEM_DefinedMultiGroupMatchesLavaan`, `TestSEM_Bootstrap*` (incl. `TestSEM_BootstrapDerivedSeed`), `TestSEM_WaldLevelMatchesLavaan`, `TestSEM_CILevelWald`, `TestRegOLS_Bootstrap_DerivedSeedStream`, `TestBrandFixture_ExamplesCohortIsTheOracleRows`, `TestExamples_SEMContributionRecipe`
- M2: `TestSEMScore_*` (incl. `TestSEMScore_MatchesLavaanPredict`, `TestSEMScore_BoundMatchesInline`, `TestSEMScore_UnseenLevelAndNullGroup`), `TestSEMFit_*` (incl. `TestSEMFit_SaveThenLoadScoresBitEqual`, `TestSEMFit_KindMismatchAndUnknown`), `TestSEMFile_*`, `TestExamples_SEMFitScoresFeedRegression`
- M4: `TestPolychoric_*` (incl. `TestPolychoric_BVNMatchesReference`, `TestPolychoric_MatchesLavaan`, `TestPolychoric_LocalAscentFromStart`, `TestPolychoric_TooManyCategories`), `TestWLSMV_*` (incl. `TestWLSMV_GammaMatchesLavaan`, `TestWLSMV_MatchesLavaan`, `TestWLSMV_GroupsMatchLavaan`), `TestMatrixSEM_Ordinal*`, `TestSEM_Ordinal*`, `TestSEM_BootstrapOrdinal*`, `TestSEMScore_EBM*`, `TestSEMScore_Ordinal*`, `TestSEM_ScaledFitIndicesInterpreted`, `TestSEMScore_OrdinalReadingInterpreted`, `TestExamples_SEMOrdinalRecipe`, `TestExamples_SEMOrdinalScores`

## Functional requirements delivered

Every FR of the effort PRD, with where it landed. None is deferred.

| FR | Delivered | Where |
|---|---|---|
| FR-1 `~1` syntax | E1-S1 | `internal/sem/parse.go`; `TestSEM_ParseIntercepts` |
| FR-2 `c(...)*x` modifiers, arity check | E1-S3 | `internal/sem/parse.go`, `group.go`; `TestSEM_ParseModifiers`, `TestSEM_InvarianceModifierLevels` |
| FR-3 single-group mean structure | E1-S1 | `internal/sem/fit.go`; `TestSEM_MeanStructureMatchesLavaan`, `TestMatrixSEM_MeanStructureMatchesLavaan` |
| FR-4 joint multi-group fit (streamable, mergeable) | E1-S2 | `internal/sem/group.go`, `internal/processing/matrix_sem_group.go`; `TestSEM_MultiGroupMatchesLavaan`, `TestSEM_MultiGroupWorkerAndShardInvariant` |
| FR-5 `group_equal` / `group_partial` (+ `thresholds`) | E1-S3, E4-S3 | `TestSEM_InvarianceProcessMatchesLavaan`, `TestWLSMV_GroupPartialThresholds` |
| FR-6 lavaan identification | E1-S3 | `TestSEM_InvarianceReferenceGroup`, `TestSEM_InvarianceReferenceLevel` |
| FR-7 lavaan conventions; legacy byte-identical | E1-S1 | `TestSEM_LegacyConventionsUnchanged`; `sem-modeling` says which convention applies when |
| FR-8 one joint `MatrixResult` | E1-S2 | `vectors.group` / `vectors.label`, per-level auxiliaries and scalars; payload-schema golden |
| FR-9 multi-group refusals | E1-S2 | `TestSEM_MultiGroupRefusals`, `TestSEM_InvarianceRefusals` |
| FR-10 predict shapes, sizes, levels | E1-S4 | `TestSEM_PredictGroupLevels`, `TestSEM_PredictGroupedMatchesRuntime` |
| FR-11 invariance-ladder recipe | E1-S5 | `sem-modeling`, the book, `compose/09_sem_invariance_sweep.json`; `TestExamples_SEMInvarianceLadderSweep` |
| FR-12 `:=` grammar and refusals | E2-S1 | `internal/sem/defined.go`; `TestSEM_DefinedRefusals` |
| FR-13 exact-gradient delta-method SEs | E2-S1 | `TestSEM_DefinedGradientExact`, `TestSEM_DefinedMatchesLavaan` |
| FR-14 `:=` rows in the multiplicity family | E2-S1 | `TestSEM_DefinedMultiplicity`; `multiplicity-correction` |
| FR-15 seeded bootstrap, percentile CIs | E2-S2, E2-S3 | `internal/sem/bootstrap.go`; `TestSEM_BootstrapDeterministic`, `TestSEM_BootstrapMatchesLavaanSampling`, `TestSEM_BootstrapDerivedSeed` |
| FR-16 bootstrap buffered; weight refused | E2-S2 | `TestSEM_BootstrapPredictBuffered`, `TestSEM_BootstrapWeightRefused` |
| FR-17 `ATTR_SEM_SCORE` (regression / Bartlett, `fit_on` XOR `fit`) | E3-S1, E3-S2 | `internal/sem/score.go`, `internal/processing/attribute_sem_score.go`; `TestSEMScore_InlineMatchesLavaan`, `TestSEMScore_MatchesLavaanPredict` |
| FR-18 compose `sem` fit kind | E3-S2 | `internal/model/sem.go`, `types/fit.go`; `TestSEMFit_KindMismatchAndUnknown`, `TestSEMFit_FitKindOf` |
| FR-19 per-group scoring, unseen level, null member | E3-S1 | `TestSEMScore_UnseenLevelAndNullGroup`, `TestSEMScore_BoundUnseenLevelByLabel` |
| FR-20 score scale | E3-S1 | `TestSEMScore_GroupedMeanIsLatentMeanShift`, `TestSEMScore_CovarianceOnlyCenteredAtFitRows` |
| FR-21 `*.pulse-sem.json` save / load, sidecar exclusion | E3-S3 | `TestSEMFit_SaveThenLoadScoresBitEqual`, `TestSEMFile_RoundTripBitExact` |
| FR-22 failed fit nulls every score | E3-S1, E3-S2 | `TestSEMScore_FailedFitNullsEveryScore`, `TestSEMFit_FailedFitScoresNull` |
| FR-23 bivariate-normal CDF | E4-S1 | `internal/sem/bvn.go`; `TestPolychoric_BVNMatchesReference` |
| FR-24 two-step thresholds, polychorics, polyserials | E4-S1 | `internal/sem/ordinal.go`; `TestPolychoric_MatchesLavaan` |
| FR-25 `estimator` / `ordered` and their refusals | E4-S2 | `TestMatrixSEM_OrdinalRefusals`, `TestSEM_OrdinalRefusalsMatchPredict` |
| FR-26 Gamma, DWLS, WLSMV robust SEs, scaled-shifted test | E4-S2 | `internal/sem/wls.go`; `TestWLSMV_GammaMatchesLavaan`, `TestWLSMV_MatchesLavaan`, `TestMatrixSEM_OrdinalMatchesLavaan` |
| FR-27 multi-group ordinal, delta only | E4-S3 | `internal/sem/wls_group.go`; `TestWLSMV_GroupsMatchLavaan`, `TestSEM_OrdinalGroupsProcessMatchesLavaan`, `TestSEM_OrdinalParameterizationRefusal` |
| FR-28 EBM scores | E4-S4 | `internal/sem/ebm.go`; `TestSEMScore_EBMMatchesLavaan`, `TestSEMScore_OrdinalInlineMatchesLavaan` |
| FR-29 `:=` and bootstrap on ordinal fits | E4-S2, E4-S3 | `brand_orddef` fixture (`TestWLSMV_MatchesLavaan`), `TestSEM_BootstrapOrdinalMatchesLavaanSampling` |
| FR-30 when to prefer WLSMV over ML | E4-S5 | `sem-ordinal` ("ML vs WLSMV — when to prefer which"), `op-mat-sem` `estimator` row, `multivariate-design` routing row, the book's "ML or WLSMV?" |
| FR-31 lavaan fixtures (R + JSON, version pinned) | every story | `internal/sem/testdata/*.R`, `*_lavaan.json` (lavaan 0.7.3) |
| FR-32 brand-survey fixture | E1-S1, E2-S3 | `internal/brandfixture`, `internal/sem/testdata/brand_spec.json`; `TestSEM_ScalarLatentMeansRecoverTruth` |
| FR-33 error codes | E2-S1, E3-S1, E4-S3 | `PULSE_MATRIX_DEFINED_NONFINITE`, `PULSE_SEM_SCORE_LEVEL_UNSEEN`, `PULSE_MATRIX_TOO_MANY_CATEGORIES` (codes, fixups, owners) |
| FR-34 Update Demand companions | every slice | next section |
| FR-35 predict parity | every slice | `TestSEM_PredictGroupedMatchesRuntime`, `TestSEM_BootstrapPredictBuffered`, `TestSEM_OrdinalPredictMatchesRuntime`, `TestSEM_OrdinalGroupsPValuesMatchPredict`, `TestSEMScore_ConfigRefusalsPredictMatchesRuntime`, `TestSEMScore_OrdinalBartlettRefusedPredictMatchesRuntime` |

## Update Demand companions

- Skills: `op-mat-sem`, `op-attr-sem-score` (new, M2), `sem-modeling` (new, M1), `sem-bootstrap` and `sem-defined-parameters` (new, M3), `sem-fits` (new, M2), `sem-ordinal` (new, M4), `compose-fits`, `vector-row-scores`, `multiplicity-correction`, `multivariate-design`, `matrix-results`
- Features: `capability:sem_meanstructure`, `capability:sem_groups`, `capability:sem_defined`, `capability:sem_bootstrap`, `capability:sem_ordinal`, `ATTR_SEM_SCORE` (`internal/descriptor/features.go`, `Since` 1.0.0)
- Guidance: `MAT_SEM` and `ATTR_SEM_SCORE` Purposes and Interpretations, including the WLSMV scaled scalars, the threshold rows and EBM scores, each scrubbed on an instance hiding `capability:sem_ordinal`
- Error codes: `PULSE_MATRIX_DEFINED_NONFINITE`, `PULSE_SEM_SCORE_LEVEL_UNSEEN`, `PULSE_MATRIX_TOO_MANY_CATEGORIES`
- Sidecar: `.pulse-sem.json` in the table-directory sidecar exclusion (`sidecar_names.go`, `byte-layout.md`)
- Reference slices: `matrix-and-vectors/matrix-sem.md`, `matrix-sem-fixtures.md`, `sem-scores.md`, `sem-ordinal.md`
- Goldens regenerated with `-update` (manifest, payload schema, public API); `format_version` stays `"1.1"`; `make docs`
- [Embedder migration](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U59)

## Handed on to later work

None. `bootstrap_iters` stays without an `Options.Limits` cap, matching a regression's `resample` (owner decision, E2-S3); a cap for both is a new limit, not a SEM change. The non-goals in Scope (FIML, theta, `.robust` indices, other estimators, modification indices, an invariance-ladder helper) are out of scope by decision, not deferred.

## Release notes (v1.0.0-alpha.26)

Draft from the migration table; behaviour changes first.

- **Behaviour changes.** A Compose `fits` entry over one `MAT_SEM` is now kind `sem` (`fits.<name>` = `{"sem": …}`); a vector consumer naming it is `PULSE_MODEL_KIND_MISMATCH`. Grouped `MAT_SEM` `vectors.group` is `[]any` (`null` on `:=` rows). A `models` path ending in neither `.pulse-model.json` nor `.pulse-sem.json` is refused with a message naming both. `NA*x` frees a parameter (it was a label named `NA`). On the lavaan path the first indicator is always the marker, `nfi` is `null` when `dof` exceeds `dof_baseline`, and a `~~` row between two variables has `std_estimate` = their residual correlation. A sparse-table polychoric is a local ascent from its start value (lavaan's figure). Ordinal `MAT_SEM` with `group` or `resample`, and Compose fits over ordinal `MAT_SEM`, are now accepted.
- **New.** `MAT_SEM` `meanstructure`, `group` / `group_reference` / `group_equal` / `group_partial`, `c(...)*x` modifiers, `:=` defined parameters, `resample: "bootstrap"` / `bootstrap_iters` / `rng_seed` / `ci_level`, `estimator` (`ml` / `dwls` / `wlsmv`), `ordered`, `parameterization` (delta only); `MAT_SEM` joins `multiplicity` through its own block. `ATTR_SEM_SCORE` (regression, Bartlett, EBM on ordinal fits), the `sem` fit kind and `*.pulse-sem.json` (an ordinal fit's file is `sem_format_version` 2). Features `capability:sem_meanstructure`, `sem_groups`, `sem_defined`, `sem_bootstrap`, `sem_ordinal`, `ATTR_SEM_SCORE`. Codes `PULSE_MATRIX_DEFINED_NONFINITE`, `PULSE_SEM_SCORE_LEVEL_UNSEEN`, `PULSE_MATRIX_TOO_MANY_CATEGORIES`.
- **Parity notes.** Every new feature is held to lavaan 0.7.3; the legacy covariance-only fit stays semopy byte-identical. The WLSMV bootstrap's oracle is lavaan's DWLS bootstrap (lavaan refuses `se = "bootstrap"` under WLSMV). EBM scores are held to lavaan's EBM objective at a tightened `reltol`, since `lavPredict`'s default stops up to ~4e-4 off the mode. A null ordinal member scores `null` (lavaan skips it); listwise is Pulse's rule.

## Human inputs & decisions

- [x] Decided in the effort interview: lavaan is the oracle for every new feature, the legacy semopy path stays byte-identical, M4 ships last so a slip never blocks M1–M3, release `v1.0.0-alpha.24` (pre-release); alpha.24 and alpha.25 were cut before merge, so it shipped as `v1.0.0-alpha.26`.
