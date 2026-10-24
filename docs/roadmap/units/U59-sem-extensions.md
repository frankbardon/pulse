---
id: U59
slug: sem-extensions
title: "MAT_SEM compares latent means across groups under invariance constraints, puts intervals on derived effects, scores latents per row and fits ordinal ratings"
track: Vector & matrix
size: L
status: in-progress
depends_on: []
soft_depends_on: [U24, U40, U42]
blocks: [U32]
todo_items: [528, 529, 530, 531, 532, 533, 534, 535, 536, 537, 538]
branch: sem-extensions
---

# U59 — sem-extensions

**Outcome:** MAT_SEM compares latent means across groups under invariance constraints, puts intervals on derived effects, scores latents per row and fits ordinal ratings.

**Track:** Vector & matrix · **Size:** L · **Depends on:** — · **Soft:** [U24](U24-matrix-operators.md), [U40](U40-compose-sweep.md), [U42](U42-fit-scoring.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`MAT_SEM` fitted a lavaan / semopy model by maximum likelihood to the members' covariance only: no mean structure, no multi-group fit, no defined parameters, no bootstrap, no per-row scores and no ordinal estimator. An analyst who compares latent means across survey waves, tests measurement invariance, puts an interval on an indirect effect, scores respondents or treats 1–5 ratings as ordinal had to fall back on heuristics.

This unit extends `MAT_SEM` in four slices, each validated against lavaan (0.7.3; ≈1e-4 relative on estimates and SEs, ≈1e-3 on fit indices). The legacy single-group covariance-only fit stays semopy-parity and byte-identical.

- **M1 (E1, done):** `meanstructure` and `~ 1` intercepts / latent means; a joint multi-group fit over `params.group` with lavaan's `group_equal` / `group_partial`, the `c(...)*x` per-group modifiers, `NA*x` and lavaan's identification. A fit that uses any of them follows lavaan's conventions.
- **M3 (E2):** `:=` defined parameters with delta-method SEs, and a seeded bootstrap with percentile intervals.
- **M2 (E3):** `ATTR_SEM_SCORE`, the compose `sem` fit kind and saved `*.pulse-sem.json` fits.
- **M4 (E4):** DWLS / WLSMV with polychoric and polyserial correlations for ordered indicators, and EBM scores.

## References

- `.claude/reference/matrix-and-vectors.md` (slice `matrix-sem.md`), `update-demand.md` (vectors-and-matrices slice), `feature-profiles.md`, `guided-analysis.md`
- `internal/skills/op-mat-sem.md`, `sem-modeling.md`, `matrix-results.md`, `multivariate-design.md`
- `docs/src/library/matrices.md` (Structural equation models)
- Examples: `internal/examples/matrices/16_sem_multigroup.json`, `internal/examples/compose/09_sem_invariance_sweep.json`
- [Embedder migration: Changes from U59](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#528** single-group mean structure: `meanstructure`, `~ 1`, ν / α, the lavaan-convention switch
- [x] **#529** joint multi-group fit: `group`, `group_reference`, per-level co-moments, the joint result, refusals
- [x] **#530** invariance: `group_equal`, `group_partial`, `c(...)*x` / `NA*x`, lavaan identification
- [x] **#531** predict, manifest and feature-profile parity (`capability:sem_meanstructure`, `capability:sem_groups`)
- [x] **#532** skills, book page, examples and the invariance-ladder recipe
- [ ] **#533** `:=` defined parameters with delta-method SEs, joining the multiplicity family
- [ ] **#534** seeded bootstrap (`resample: "bootstrap"`), percentile CIs, buffered, weight refused
- [ ] **#535** `ATTR_SEM_SCORE` (regression / Bartlett), inline `fit_on` or a bound `fit`
- [ ] **#536** the compose `sem` fit kind and `*.pulse-sem.json` save / load
- [ ] **#537** DWLS / WLSMV with thresholds, polychoric and polyserial correlations
- [ ] **#538** EBM scores of ordinal SEM fits

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
### E2 — Analyst gets intervals on derived effects
- S1 defined parameters with delta-method SEs, S2 seeded bootstrap, S3 contract, docs and example
### E3 — Analyst scores latents per row and reuses SEM fits
- S1 `ATTR_SEM_SCORE` from an inline fit, S2 the compose `sem` fit kind, S3 save and load, S4 contract, docs and example
### E4 — Analyst fits ordinal ratings with WLSMV
- S1 thresholds, polychoric and polyserial correlations, S2 DWLS / WLSMV single group, S3 ordinal multi-group and bootstrap, S4 EBM scores, S5 contract, docs and release notes

## Acceptance criteria

- [x] ML mean structure and the configural / metric / scalar / partial multi-group fits match lavaan within tolerance; the scalar fit on the brand-survey fixture recovers the wave-2 latent mean shifts within 2 SE
- [x] Legacy single-group covariance-only output is byte-identical (semopy parity unchanged)
- [x] The invariance ladder reads from fit indices across fits; a compose sweep over `group_equal` runs it (`compose/09_sem_invariance_sweep.json`)
- [ ] `:=` contributions get delta-method or bootstrap intervals and p
- [ ] SEM latent scores are usable downstream; scoring a saved fit is bit-equal to scoring the in-compose fit
- [ ] DWLS / WLSMV matches lavaan, and the docs say when to prefer it over ML
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestManifest*Complete`, `TestSkillTokenBudget`, `TestSkillsCoverFeatureFences`, `TestSkillParamsMatchManifest`, `TestEveryOperatorHasAnExampleTag`, `TestDocsGeneratedCurrent`
- M1: `TestSEM_MeanStructureMatchesLavaan`, `TestSEM_MultiGroupMatchesLavaan`, `TestSEM_ScalarLatentMeansRecoverTruth`, `TestSEM_LegacyConventionsUnchanged`, `TestSEM_InvarianceComposeSweep`, `TestSEM_HiddenSurfaces*`, `TestSEM_PredictGroupLevels`, `TestExamples_SEMInvarianceLadderSweep`

## Update Demand companions

- Skills: `op-mat-sem`, `sem-modeling` (new, M1), `matrix-results`
- Features: `capability:sem_meanstructure`, `capability:sem_groups` (`internal/descriptor/features.go`)
- Goldens regenerated with `-update` (manifest, payload schema, public API); `format_version` stays `"1.1"`
- [Embedder migration](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U59)

## Handed on to later work

None from M1. Later epics add their hand-ons here.

## Human inputs & decisions

- [x] Decided in the effort interview: lavaan is the oracle for every new feature, the legacy semopy path stays byte-identical, M4 ships last so a slip never blocks M1–M3, release `v1.0.0-alpha.24` (pre-release).
