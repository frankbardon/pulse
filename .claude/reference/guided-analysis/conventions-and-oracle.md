# Guided analysis — Convention registry, statistical primitives and the R oracle, review record, extension hook, status, out of scope

Per-topic slice of the guided-analysis reference (Convention registry, statistical primitives and the R oracle, review record, extension hook, status, out of scope). Index (preamble + section map): `.claude/reference/guided-analysis.md` — load it first, then only the slice(s) your change touches.

## Convention registry

`internal/descriptor/conventions.go` (`builtinConventions`) is the ONLY source of built-in `Interpretation.Bands`: an Interpretation calls `conventionBands(id)` and `conventionCitation(id)` and never writes thresholds inline. Each entry carries an ID, a citation, the `Statistics` it may band, ascending `Thresholds`, `Labels` (one more than thresholds — the lowest band below Cohen's "small" is labelled `very small`, never "negligible" or "none"), `Abs` (bands read |value|) and `SymmetricLog` (a ratio statistic whose bands apply to max(x, 1/x)).

| ID | Statistics | Thresholds | Source |
|---|---|---|---|
| `cohen1988_d` | `cohens_d`, `hedges_g`, `glass_delta`, `cohens_h` | 0.2 / 0.5 / 0.8, `Abs` | Cohen (1988) |
| `cohen1988_eta2` | `eta_squared`, `omega_squared` | 0.01 / 0.06 / 0.14 | Cohen (1988) |
| `cohen1988_r` | `pearson_r` | 0.1 / 0.3 / 0.5, `Abs` | Cohen (1988) |
| `cohen1988_w` | `cohens_w`, `phi` | 0.1 / 0.3 / 0.5 | Cohen (1988) |
| `cohen1988_or` | `odds_ratio` | 1.44 / 2.48 / 4.27, `SymmetricLog` | Cohen's d benchmarks via d = ln(OR)·√3/π (Chinn 2000) |
| `cohen1988_r2` | `r_squared` | 0.02 / 0.13 / 0.26 | Cohen's f² benchmarks via R² = f²/(1+f²) (Cohen 1992) |

**Fixture contract.** `testdata/conventions.json` is transcribed independently from the sources, never generated from the Go registry. `TestConventionRegistryMatchesFixture` holds the two equal field by field, requires an https source URL plus a page / section locator per entry, and recomputes every derived threshold (η² from f, OR from d, R² from f²). Its `statistic_bindings` map a banded non-effect-size output to a registry statistic, and its `excluded` list names every output left unbanded on purpose WITH a reason (Interpretation section above). `TestBuiltinBandsCiteRegisteredConvention` binds each banded built-in Interpretation to exactly one registered convention (matched on Bands, Convention and `Abs`) whose `Statistics` cover the banded output, and refuses bands on an `excluded` output. Adding a convention means: source it, add the fixture entry and the registry entry in one change; an output with no sourced convention stays unbanded and carries a "no sourced bands" caveat (lint rule `ES-CONV`).

## Statistical primitives and the R oracle

Every p-value, critical value and interval rests on a small set of shared distribution primitives. Their numeric correctness is pinned to R, not to hand-pasted literals.

- **Generator.** `scripts/reference/gen_reference.R`, run by `make reference` (R 4.6.1 + `jsonlite`), writes one JSON per primitive into `internal/processing/testdata/reference/` — the R and package versions, the R expression, and every case at `%.17g` — then appends the `// golden-hash:` footer processing's `TestGoldensNotHandEdited` checks. **CI never runs R**; the goldens are committed. Where stock R is less accurate than the oracle needs (`ptukey` / `qtukey`, far-tail `qt`), the script refines in R (nested quadrature, Newton-polished quantiles) and keeps R's own value beside it (`r_ptukey_upper`, `r_qt`, …). Recipe: `docs/src/internals/regenerating-goldens.md`.
- **Multivariate goldens (U24).** `gen_reference.R` sources `scripts/reference/gen_multivariate.R`, which writes `mv_*.json` (rank / partial correlation, alpha + ω, one-factor minres, PCA + KMO + Bartlett, VIF + `colldiag`, `vcov(lm/glm)`, `nearPD`) on vendored `attitude` / `mtcars` plus seeded fixtures under `testdata/reference/fixtures/` (CSV + `.pulse` via `internal/tools/refcohorts`), with `psych` / `car` / `ppcor` / `perturb` / `corpcor` / `Matrix` pinned in its header. Consuming tests land with each matrix operator.
- **Tests.** `TestReferenceOracle_*` (`internal/processing/reference_oracle_test.go`, `internal/statdist/reference_oracle_test.go`) cover the Student-t family, `chiSquareSurvival`, `fSurvival`, `standardNormalCDF`, `standardNormalPPF`, `kolmogorovSurvival`, `studentizedRangeSurvival` and `studentizedRangeInverse`, with p down to ~1e-300.
- **Tolerance policy.** Relative `1e-10` (an absolute floor only at the representable tail edge). Never an absolute tolerance on a p-value, and never a looser one to make a case pass: amd64 and arm64 fuse `a*b+c` differently, so values drift by ulps and a relative bound is the only portable one. **A failure means the Go primitive is wrong — fix it; regenerate only to change the grid** (script and goldens committed together).
- **Normal tails.** A two-sided normal p is `normalTwoSidedP(z)` = `erfc(|z|/√2)` and a one-sided upper tail `normalUpperTailP(z)` = Φ(−z); never `2·(1 − Φ(|z|))`, which cancels to exactly 0 past |z| ≈ 8.3. Every normal-approximation family has a `*_TinyP` test at |z| ≈ 10 against R's `2*pnorm(-|z|)` (`internal/processing/normal_tail_test.go`, `TestNormalTwoSidedP_MatchesR`).
- **`internal/statdist`.** The Student-t family (`StudentTTwoSidedP`, `StudentTCDF`, `StudentTQuantile`, `StudentTInverseTwoSided`) and the regularized incomplete beta live once, in a leaf importable by `internal/processing` and `internal/processing/regression` (stdlib + gonum only, `TestStatdistImportBoundary`). `TestStudentTFormsAgree` pins the paired forms bit for bit. Helpers keep their function boundaries on purpose (FMA bit-identity). Chi-square, F, normal, Kolmogorov and studentized range still live in `internal/processing`; moving one to `statdist` is a refactor that must stay bit-identical.
- **Not yet covered.** The oracle pins the PRIMITIVES, not each operator's assembled output. Per-output numeric oracles (Shapiro–Wilk, Brown–Forsythe, Tukey q / `p_adj`, KS p, Kendall τ-b with ties, Mann–Kendall p, Pearson CI, every `TEST_*` p at tight tolerance, `REG_OLS` / `REG_GLM` SE + p, the `REG_BAYES_LINEAR` posterior, de-circularised `OVERLAY_CHISQ_VS_POP` / `OVERLAY_KS_VS_POP`, `OVERLAY_PAIRWISE_PROBIT_T`) are U36.

## Review record

The statistics review of the U08 guidance is committed at `docs/roadmap/reviews/U08-statistics-review.md`: method (the deterministic layers above plus an advisory LLM panel), every finding with its disposition and fixing commit, the engine fixes it triggered, and the open items for the human reviewer. Human sign-off is a release-blocking U33 item; a later guidance backfill appends its own section rather than starting a new file. The weighted-inference review (U12: n_eff semantics and every lifted weighted formula — engine, not guidance, so its own file) is `docs/roadmap/reviews/U12-weighting-review.md`, on the same U33 #206 sign-off list.

## Extension hook

All eight root `*Registration` structs take an optional `Purpose *descriptor.Purpose`; `TestRegistration` also takes `Interpretation []descriptor.Interpretation`. At every `pulse.New`, AFTER naming/shape validation, the probe and the `DependsOn` check, `validateExtensionGuidance` (`extensions_guidance.go`) runs the SAME `ValidatePurpose` the built-in tier uses (plus cross-operator `KnownAs` uniqueness against built-ins and earlier extensions), resolving `NotFor.Use` and `FollowUps.Use` against the instance (built-ins, the feature table AND other extensions), then `ValidateInterpretations` in STRUCTURE-ONLY mode (nil resolver: path syntax and content, never key existence). First invalid wins (category order, then slice index; Purpose before Interpretation) → `PULSE_EXTENSION_PURPOSE_INVALID`, details `category`, `name`, `index`, `part`, the first failing rule and every violation. Only the Purpose's sorted intent IDs project to `OperatorMeta.intents`; prose never enters the manifest (`TestExtensions_PurposeProseNeverInManifest` — per instance, so outside `GuidanceProse`). An absent Purpose projects no `intents` key and keeps the extension out of the coverage report by construction. Embedder prose: `docs/src/internals/extension-points.md`.

## Status (U21 delivered)

Delivered: `internal/docgen` and the committed book tree `docs/src/guide` (`make docs`, bound by `TestDocsGeneratedCurrent`); the read-time `## Use when` / `## Reading the output` skill sections (`skill-pack.md` Generated sections, `TestSkillPurposeSectionsCurrent`); `p.ExportReference` / `pulse docs export` (`feature-profiles.md` Reference export); and the slot-token filter (`slotTokens`, `feature-profiles.md` Instance-dependent wording) that keeps `multiplicity`, `p_adjusted`, `matrices` and the other hidden capability tokens out of every rendered sentence. Any Purpose, Interpretation, glossary or `ComponentSchema` edit therefore ends with `make docs` in the same PR. Generated prose stays out of the default manifest, `Response` and `PredictResult` (`TestManifestGuidanceBudget`).

## Out of scope (owned later)

- U24 / U25 / U27 / U30 — the ledger's remaining owners: the `measure_construct` and `flows` intents plus the glossary terms their operators will link (Gates, exemption ledger). Each closes its own entries; a covered gap or an owner marked `status: done` makes the entry fail as stale.
- U36 also owns the runtime defects U09 documented rather than fixed (split-aware `FEAT_TARGET_ENCODE`, `AGG_ZSCORE`'s always-zero value, normal-z `AGG_CI_*`, tied `ATTR_PERCENTILE`, attributes writing 0 for missing inputs) — the Interpretations and skills describe today's behaviour.
- U10 — profile-aware skill rendering.
- U38 — syncing the hand-written atomic skills that drifted from the registries (U21 delivered the rendering; see Generated reference above and `skill-pack.md` Generated sections).
- U22 — Recommend (unbound and cohort-bound) and Explain request mode plus response mode over every result root: sections above; the CLI / MCP surfaces, payload roots and goldens land later in U22.
- U23 — guidance over MCP.
- U33 — release-blocking human statistics sign-off of the U08 review record (`docs/roadmap/reviews/U08-statistics-review.md`), with the reviewer's CODEOWNERS entries.
- U36 — per-output numeric oracles (Statistical primitives and the R oracle, "Not yet covered").
