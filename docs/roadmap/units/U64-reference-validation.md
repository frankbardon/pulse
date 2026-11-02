---
id: U64
slug: reference-validation
title: "Every inferential number is traceable to an external reference, the gaps are closed, and users can see the evidence"
track: Statistical integrity
size: L
status: in-progress
depends_on: []
soft_depends_on: [U61]
blocks: [U32, U65]
todo_items: [588, 589, 590, 591, 592, 593, 594, 595, 596, 597, 608, 609, 610, 611, 612]
branch: reference-validation
---

# U64 — reference-validation

**Outcome:** Every inferential number is traceable to an external reference, the gaps are closed, and users can see the evidence.

**Track:** Statistical integrity · **Size:** L · **Depends on:** — · **Soft:** [U61](U61-cross-arch-determinism.md) · **Unblocks:** [U32](U32-docs-audit.md), [U65](U65-regression-depth.md)

## Summary

Stata and SPSS win on trust: decades of validation and published certification results. Pulse's evidence is already strong, but nobody outside the test suite can see it.

What exists today:
- **R goldens.** `make reference` pins R 4.6.1 and its packages. Goldens are written at `%.17g` with a hash footer, and the Go tests compare against them at a relative tolerance of 1e-10.
- **Weighted reference** at 1e-12: R on `rep()`-expanded rows for frequency weights, and a closed form with scipy and statsmodels for probability weights.
- **SEM** is checked against lavaan and semopy.
- **Other packages:** `optimize` against minpack.lm and nloptr, and `mcmc` against ArviZ.

This unit does three things. It publishes that evidence. It closes the gaps the inventory found, and it adds the certification benchmark that the established packages cite, NIST StRD. Finally it carries the evidence into Studio.

**Inventory gaps, found 2026-10-31:**
1. **MMM regressions.** `REG_MMM_HILL` and `REG_MMM_BAYES` have no external reference, only recovery of known synthetic parameters.
2. **Loose unweighted pins.** `TEST_T` and `TEST_WELCH` are checked only against a 4-decimal scipy value. `TEST_Z_TWO_SAMPLE`, `TEST_CHISQ`, `TEST_MANN_WHITNEY_U` and `TEST_KRUSKAL_WALLIS` are checked only against hand-computed values. The tight pin for all six is the weighted reference.
3. **Unweighted overlays.** Unweighted `OVERLAY_CHISQ_ROW`, `_COL` and `_MATRIX` have internal checks only.
4. **No weighted reference** for `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK`. That is either a refusal to pin or a weighted golden to add.
5. **No certification datasets.** NIST StRD is the regression and ANOVA accuracy benchmark that Stata, SPSS, SAS and R are all measured against, and Pulse has none of it.

## References

- `docs/src/internals/regenerating-goldens.md` (R toolchain, weighted reference, multivariate goldens)
- `internal/descriptor/validation_registry.go` (the registry, since E1-S2) + `internal/docgen/validation.go` (the book's rendering) + `validation_intro.md`; `.claude/reference/guided-analysis/explain-and-skills.md` (Operator mode, Validation page)
- [U08 statistics review](../reviews/U08-statistics-review.md) (the R-oracle layer); [U61](U61-cross-arch-determinism.md) (identical bits across architectures)
- NIST Statistical Reference Datasets: <https://www.itl.nist.gov/div898/strd/>

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#613** The book's "How Pulse is validated" page and a "Validated against" list on every inferential operator's catalog entry, generated from a gated registry (E1-S1)
- [x] **#614** Validation evidence reachable from the engine (pulled, never pushed), so Studio and agents can show it (E1-S2)
- [x] **#615** The registry gate checks that a cited test actually exercises the operator, not only that the file exists (E1-S3)
- [x] **#616** R goldens at 1e-10 for the unweighted `TEST_T`, `TEST_WELCH`, `TEST_Z_TWO_SAMPLE`, `TEST_CHISQ`, `TEST_MANN_WHITNEY_U`, `TEST_KRUSKAL_WALLIS`, `TEST_PROP_Z` (E2-S1)
- [x] **#617** R goldens for the unweighted `OVERLAY_CHISQ_ROW` / `_COL` / `_MATRIX`; weighted goldens or a pinned refusal for `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK` (E2-S2)
- [x] **#618** NIST StRD linear regression (all 11 datasets, Norris to Filip and Longley), one-way ANOVA (all 11) and univariate summary statistics (all 9) as goldens, with the certified log relative error (LRE) per dataset published (E3)
- [x] **#633** `TEST_ANOVA_F` reaches the double-precision ceiling on NIST SmLs07–09: 4.0 / 3.9 / 3.9 digits (3.2 before). The original 7-digit target is impossible from float64 input; see E3-S4 (E3-S4)
- [x] **#636** Shifted accumulation for the other per-group Welford tests where no parity contract forbids it: `TEST_ANOVA_WELCH` and one-sample `TEST_T`. The rest don't need it: `TEST_BROWN_FORSYTHE` reads deviations, the paired tests read differences, and `TEST_TUKEY_HSD` and the post-tests read materialized means (E3-S7)
- [ ] **#637** Decide, then do: two-sample `TEST_T` / `TEST_WELCH` / `TEST_Z_TWO_SAMPLE` must stay byte-equal with `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` / `_VS_REF`, which fold unshifted `AGG_WELFORD` cells. They lose about 3 digits under a 1e12 offset (1e-9 relative at 1e6). Precision there means shifting `AGG_WELFORD` itself, merge across shards included, so both sides move together (owner decision D4)
- [x] **#634** `REG_OLS` solves on a streaming Givens QR (`ols_qr.go`, AS 274) instead of Cholesky on the normal equations. NIST Filip now fits (7.4 digits; it was refused), and Wampler1–5 reach 9.6 / 13.3 / 9.7 / 7.7 / 5.7 (6.0–6.7 before), level with LAPACK's Householder QR (8.1, and 7.6 / 5.6 on Wampler4 / 5). Wampler5's 7-digit target is beyond double precision for any solver (E3-S5)
- [x] **#635** Regression through the origin, `intercept: false` on `REG_OLS` (default `true`). NIST NoInt1 / NoInt2 reach 14.1 / 15 digits. `solver: "qr" | "normal"` (default `qr`) restores the normal-equations path on request. Both are refused by the shared rule `internal/regsolve`, at predict and at run (E3-S6)
- [ ] **#644** Decide, then do: the methodological options the E2-S1 pins surfaced, each a candidate knob under the configurability rule with today's behaviour as its default — a pooled-variance (Student) two-sample `TEST_T`, an exact `TEST_MANN_WHITNEY_U` p, Yates' correction on a 2 × 2 `TEST_CHISQ`, and the Wilson interval for the one-proportion `TEST_PROP_Z` (E2-S1 follow-up)
- [x] **#619** `REG_MMM_HILL` pinned to R `minpack.lm::nlsLM` on the same objective and starting values (E4-S1)
- [x] **#620** `REG_MMM_BAYES` pinned in its conjugate limit to `REG_BAYES_LINEAR`, plus a seeded, reproducible simulation-based calibration harness (E4-S2)
- [ ] **#621** Studio shows each operator's validation in the inspector and links the book page (pulse-studio, E5-S1)
- [ ] **#622** Studio's own arithmetic (the workflow Stat test and Fit statistics nodes) appears in a Studio validation table, with its R goldens cited (pulse-studio, E5-S2)

## Decisions (owner, open)

- **D1 — where the engine exposes validation (#614).** The options:
  - (a) A read-time surface such as `Explain`, with no manifest bytes, so the guidance budget is untouched.
  - (b) An additive `validation` block per operator in the manifest.
  - (c) A JSON file beside `pulse docs export`.

  Recommended: (a). It is pulled rather than pushed, and Studio already embeds Pulse as a library.
- **D2 — NIST failure policy.** If a hard dataset (Filip, Wampler 4–5) is below the certified accuracy, is that a published known limitation or a blocker? Recommended: publish the LRE either way, and fix anything under 7 digits on the "average" and "lower" difficulty datasets.
- **D4 — two-sample precision versus overlay parity (#637).** The two-sample mean tests are byte-equal with their cell overlays by contract. Both read Welford means held at the data's magnitude, so both lose precision under a large common offset. The options:
  - (a) Shift `AGG_WELFORD` and the overlay fold together, keeping parity and gaining precision. This touches the aggregator, its shard merge and every overlay golden.
  - (b) Keep parity, and keep the loss.
  - (c) Shift only the tests, and drop byte parity for a 1e-12 tolerance.

  Recommended: (a). Survey data rarely sits at 1e12, but revenue in cents or epoch timestamps can.
- **D3 — v1 timing.** Recommended: E1–E3 land before U32 (they are trust evidence for v1), and E4–E5 may follow v1.0.0.

**Decided 2026-11-01** (the owner delegated these choices):

- **D1 → (a).** Validation is a read-time surface on `Explain` (`pulse explain OP`, `pulse_explain`). It adds no manifest bytes and moves the registry into `internal/descriptor`.
- **D2 → publish the digits either way.** Fix anything under 7 digits on the lower- and average-difficulty datasets. Applied: SmLs07–09 and Wampler5 sit at the double-precision ceiling and are published as such.
- **D3 → E1–E3 before U32; E4–E5 may follow v1.0.0.**
- **D4 → (a).** Shift `AGG_WELFORD` and the moment-overlay fold together, keeping byte parity and gaining the precision. This includes the shard merge, so that partials with different origins combine exactly. It ships as #637, after U65 E1.

**Decided 2026-11-02** (owner-delegated, 2026-11-02):

- **E1-S2: operator mode is a new Explain root, not a block on every finding.** A `validation` block on each step or finding would push the evidence into every Explain answer and move every Explain golden. A root keeps it pulled, and it is the one call Studio's inspector needs per operator node.
- **E1-S2: NIST digits travel with the record.** The StRD references say "digits achieved per dataset (see the NIST table)", which an agent cannot follow. So `certification` carries the rows, each restricted to the figures the operator produces: `AGG_AVERAGE` gets `mean`, `AGG_STDDEV` and `AGG_WELFORD` get `sd`, so neither reports the other's digits.
- **E1-S2: refusals and scrubbing.** An unknown or hidden operator is `SERVICE_VALIDATION` with `details.field: operator`, the code Explain already uses for its input, so no new error code was added. The instance view passes every string through the instance's prose scrub and drops a reference the scrub empties, as every other Explain sentence does. The gap and the refusal reason stay verbatim in `validation`, as refusals and warnings do. The caveats only point at them by template, so the binding prose lint covers every Explain-authored sentence.
- **E1-S2: an operator with no record is not an error.** An extension, or a descriptive operator outside the record, returns no `validation` and a caveat saying Pulse holds no record. That is a different fact from "not offered".
- **E1-S3: the exercise rule accepts runners and kernel oracles beside a naming file.** A strict "every file names the operator" rule would forbid the repo's deliberate pattern: kernels pinned against R once, then routing tests that run them. Instead, a file that does not name the operator passes only through a sibling citation in the same package that does, and only by sharing a declaration (a table it reads) or a non-test function (the kernel). A file cited alone must name its operator itself.

- **E2-S1 script placement.** The unweighted pins live in a new standalone script, `gen_unweighted_tests.R`, not in `gen_test_outputs.R` as S1 sketched. `gen_test_outputs.R` cannot run alone (it needs `gen_reference.R`'s quadrature and the `Kendall` and `nortest` pins), and regenerating it would rewrite nine unrelated goldens. The new script needs base R only and moves no other golden.
- **E2-S1 "both variances".** Pulse offers no pooled-variance two-sample t, so `t.test` is pinned on its one-sample and Welch forms. Adding `var_equal` (and the other options the pins surfaced) is a new methodological knob, not a validation fix; it is #644, for the owner.
- **E2-S1 near-null Kruskal-Wallis.** Where stock R loses digits to cancellation the golden pins the exact deviation-form value and keeps R's own beside it, the pattern Fisher and Tukey already use. The engine was fixed to match rather than the tolerance loosened.

- **E2-S2: refusal over a weighted golden for `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK`.** A `rep()` expansion is not a meaningful weighted repeated-measures ANOVA: copies of a subject-by-condition row become within-cell replicates, which changes the model `aov(Error())` fits. A frequency-weighted Shapiro-Francia W′ is computable on expanded rows, but its p would come from Royston's calibration applied to a tie-heavy sample it was not built for, and no probability-weighted form exists. Both refusals were already permanent and stated; this story pins them and publishes them. Lifting either is new weighted methodology, not validation.
- **E2-S2: a `WeightRefused` registry field, not `Gap` prose.** A refusal named in `Gap` text would cite a test file no gate checks, and would show on instances that hide weighting. The field is gated, takes its wording from the engine's own refusal reason and follows the weighting feature. `TEST_TREND` and `TEST_TUKEY_HSD` (post-test only) state their refusal too, on owner request: every weighted post-test is refused with the shared post-test reason (the result rows carry no row weights), not the operator's own `RefusalReason`, so their records set `PostTestOnly` and quote `descx.PostTestWeightRefusal`. The pin covers them; their opt-out run (Tukey over every subject group, seconds) stays with the heavy `TestWeight_InferentialRefusalsMatchPredict`, and the fast pin checks the opt-out in predict.
- **E4-S1: tolerance 1e-6, not 1e-10.** The gap is Pulse's Levenberg–Marquardt stop rule (relative cost change 1e-13, relative step 1e-10, the `optimize.LeastSquares` defaults), magnified along the β–k–s valley; tightening the rule to 1e-16 / 1e-14 brings the gap to 3.7e-9 / 9.7e-9, so the objective, Jacobian and covariance agree. 1e-6 is the optimize oracles' tolerance and more than 10× the evidence; each falsification misses it by 300× or more. The stop rule stays: a 1e-8 relative move is far inside every standard error, and tightening it would add iterations to every sweep fit for no figure a user reads.
- **E4-S1: the polished stationary point is the stored value, with nlsLM's own estimates kept as `r_nlslm`.** nlsLM's finite-difference Jacobian pins x only to about 3e-8, the same reason `gen_optimize.R` stores the analytic `nls.lm` fit.
- **E4-S1: standard errors as REG_MMM_HILL defines them.** σ² = SSR / (n − p) with SSR over the data rows only and p every parameter, a held channel's three included, and JᵀJ including the prior row. These equal `summary(nlsLM)`'s on the prior-free, unheld cases (checked to 1e-6 in the script).

- **E4-S2: the conjugate limit is a flat-prior limit, and the SD comparison carries a stated factor.** REG_MMM_BAYES's priors are independent (β's prior is not scaled by σ), so no setting of them is the NIG prior; both models meet only where every prior is flat. There they differ in σ's flat prior: flat in σ for REG_MMM_BAYES, the σ^−p factor of a vanishing Λ₀ for NIG. REG_BAYES_LINEAR cannot express REG_MMM_BAYES's limit, which would need a negative `prior_shape`. So the test compares the means directly and the SDs through √(DF / (ν − 2)), an exact consequence of the two priors, not a fudge. The cohort's 60 rows make the factor 1.054, so a wrong df fails the check.
- **E4-S2: "flat saturation and fixed adstock" is read as pinned decay, k and s.** No Hill curve is exactly linear in spend, so the curve is held fixed at the cohort's own k and s and REG_BAYES_LINEAR regresses on that curve. Each is pinned with a truncnormal 1e-9 wide rather than a new "fixed" option: there is no engine change, and the sampler's unconstrained coordinate stays well scaled.
- **E4-S2: Monte Carlo tolerance, seeded.** The tolerance is 4 Monte Carlo SEs, from the run's own min(bulk, tail) ESS, an SD error from the t kurtosis, and a normal-theory error for σ's SD. The run is seeded, so the verdict is fixed; with any seed an honest sampler would pass all eight checks about 99.95% of the time.
- **E4-S2: SBC at a fixed outcome scale.** The default priors are stated on the max-scaled outcome, so they move with max|y|, an empirical-Bayes scaling that no generative prior reproduces. The harness therefore fixes the scale at 1 through a test seam (`sbcModel`), which calibrates the whole posterior computation: the density, transforms, Jacobians, MAP start and NUTS. The data-dependent scaling itself is a modelling choice, stated in the registry gap. The rank histograms are published only here, with their harness and seed (U63's rule).
- **E4-S2: no new TODO for an external MCMC cross-check.** A Stan or PyMC-Marketing comparison of the full nonlinear model would need a non-R toolchain this unit does not pin, so it stays a published gap until the owner asks for it.

## Configurability (owner rule, 2026-10-31)

An operator exposes a parameter wherever there is a real methodological choice, and its default is the current behaviour. Every story in this unit records its options and defaults. A pure accuracy fix with no result a user would choose differently (for example the origin shift in the mean tests) is not a knob. A contract between operators (byte parity) is not a per-request knob either. Applied so far:

- `REG_OLS` `intercept` (default `true`) and `solver` (`qr` default, `normal`).
- `AGG_STDDEV` / `AGG_VARIANCE` `ddof` (0 default, 1 sample).

## Scope

**In scope**
- The registry, the book page and the per-operator lists (E1-S1, done on this branch), plus the engine surface and the stronger gate
- New R goldens for every unweighted gap, and weighted goldens or pinned refusals for the two unweighted-only tests
- NIST StRD goldens and the published LRE table
- External references for the two MMM regressions
- Studio: the inspector view, and validation of Studio's own arithmetic

**Out of scope**
- New operators or outputs (see [U65](U65-regression-depth.md); every one it adds must arrive with its registry row)
- Running R in CI (it stays a generator only)
- Comparing against Stata or SPSS binaries. Their licences forbid shipping their outputs as goldens, so NIST certified values are the neutral reference instead.

## Epics & stories

- **E1 — Publish the evidence**
  - S1 *(done on this branch)*:
    - The `internal/docgen/validation.go` registry, `docs/src/guide/validation.md`, and a "Validated against" list plus a "Validation gap" line in each catalog detail block.
    - Gates: `TestValidationRegistryCoversInferential`, `TestValidationRegistryNamesBuiltins`, `TestValidationReferencesExist`, `TestValidationDropsWeightedWhenHidden` (the first three moved to `internal/descriptor` in S2).
  - S2: the engine surface chosen in D1. If it is (a), `pulse explain OP` and `pulse_explain` carry the operator's references and gap. The registry moves to `internal/descriptor` so docgen and the surface share it.
  - S3: the gate checks that each cited test file names the operator, or the golden file that does.
  - S2 *(done)*: #614.
    - **The surface.** Explain gains an operator mode: `ExplainRequest.Operator` set alone (`pulse explain OPERATOR`, `pulse_explain {operator}`) returns `mode` / `root` `operator` and `ExplainResult.Validation`, a `descriptor.OperatorValidation`: `references[]` `{against, tolerance, tests, weighted}`, `gap`, `weight_refusal` `{code, reason, tests}` and `certification[]`, the NIST StRD rows `{family, dataset, difficulty, digits, min_digits}`. Summary counts the references; template caveats point at a gap and a weight refusal; full detail adds the operator's glossary terms, follow-ups and assumptions.
    - **One record.** The registry moved to `internal/descriptor/validation_registry.go` and the StRD copy to `internal/descriptor/strd_lre.json`. `InstanceSnapshot.Validation` is the instance view both docgen and Explain read, so the book did not move a byte through the move. The registry gates moved with it (`internal/descriptor/validation_test.go`); docgen keeps its two rendering tests.
    - **Wire.** Additive only, so `format_version` stays `"1.1"`: an `operator` request slot, a `validation` result slot, the `operator` value on `ExplainMode` and `ExplainRoot`, and four public types (`OperatorValidation`, `ValidationReference`, `ValidationWeightRefusal`, `ValidationCertification`). No manifest byte moved, so `TestManifestGuidanceBudget` is untouched. The payload-schema, API and new `explain.operator.<detail>.json` goldens were regenerated.
    - **Pinned by** `TestExplainOperator_*` (`internal/guide`; falsified on the validation slot, the visibility check, the weighting drop and the per-operator figure filter), `TestValidation_ScrubsHiddenNames` (falsified on the scrub), `TestPulseExplain_OperatorMode`, `TestCliExplain_Operator`, and the MCP invisibility sweep, which now calls operator mode for every recorded operator a profile offers.
  - S3 *(done)*: #615.
    - **The rule.** `TestValidationReferencesExist` now requires every cited file to exercise its operator. A file passes when it names the operator by wire name or by a `types` constant spelling it (`OverlayKindChiSqRow`), or when a string literal in it names a golden under its package's `testdata/` (by path, base name or stem) that does. Two harness shapes reach the operator only through a file cited beside them in the same reference and package that does name it: a runner that uses a top-level name that file declares (the weighted runner and its values table), and a kernel oracle that calls a non-test package function the routing test also calls (the shared two-sample kernels). `TestValidationExercisesRule` pins the rule and was falsified on the golden and kernel branches.
    - **What it caught.** Four citations named a file that never ran the operator. `OVERLAY_PAIRWISE_TWO_MEANS_Z` cited `overlay_pairwise_test.go`, which ran only the Welch t and proportion kinds, so its R pin had no harness. It now has one: `TestOverlayPairwise_TwoMeansZ_Welford` runs the overlay on Welford legs against R's `2 * pnorm(-|z|)` from `overlay_two_sample.json`, and was falsified by routing the kind to the Welch kernel. `OVERLAY_PROP_Z_CELL` cited the same file. It now cites the kernel oracle (`prop.test` on `twoProportionZ`) and the routing test that runs that kernel on each cell's legs. `MAT_SEM` cited `internal/sem/lavaan_test.go`, an estimator-level check that never names the operator. It now cites the end-to-end lavaan and semopy tests (single- and multi-group ML, ordinal WLSMV / DWLS, bootstrap). `MAT_FACTOR` dropped `matrix_minres_test.go`, a kernel test whose claim the two operator-level psych tests already carry.
- **E2 — Close the unweighted gaps**
  - S1: `scripts/reference/gen_test_outputs.R` gains `t.test` (both variances), `prop.test(correct = FALSE)`, `chisq.test(correct = FALSE)`, `wilcox.test` (exact and normal-approximation arms, matching Pulse's tie rule), `kruskal.test` and a `pnorm` two-sample z. Each test pins its statistic, df, p and interval at 1e-10. The registry rows drop their "Gap".
  - S2: `chisq.test` goldens for the three chi-square overlays on unweighted crosstabs. For `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK`, either a weighted golden (`rep()` expansion where the test admits frequency weights) or a test pinning the refusal code, plus a registry row stating which.
  - S1 *(done)*: #616.
    - **The script.** `scripts/reference/gen_unweighted_tests.R` (base R + `jsonlite`, sourced last by `gen_reference.R`, runnable on its own) writes `test_t.json`, `test_z_two_sample.json`, `test_chisq.json`, `test_mann_whitney_u.json`, `test_kruskal_wallis.json` and `test_prop_z.json`. `internal/processing/test_unweighted_oracle_test.go` replays every case through the operator's registered row test at relative 1e-10, and each oracle was falsified (a df, critical value, continuity branch, tie variance or interval SE perturbed: the test failed).
    - **Worst relative error** (statistic, df, p, interval, means and variances): `TEST_T` / `TEST_WELCH` 1.0e-13, `TEST_Z_TWO_SAMPLE` 1.3e-13 (both on the 1e3-offset case), `TEST_CHISQ` 4.7e-15, `TEST_MANN_WHITNEY_U` 6.6e-15, `TEST_KRUSKAL_WALLIS` 9.7e-16, `TEST_PROP_Z` 4.1e-14. No case puts the two-sample tests on a 1e6 offset: that is #637.
    - **Engine fix: `TEST_KRUSKAL_WALLIS` near H = 0.** H was evaluated as 12/(N(N+1)) Σ R²/n − 3(N+1), which cancels as H → 0: on 300 ranks with equal rank sums broken by one tie (H = 6.6e-7) both Pulse and stock `kruskal.test` were 1.4e-8 relative off the exact value. Pulse now evaluates Σ (R − n(N+1)/2)² / n, exact on unweighted mid-ranks, and reaches 9.7e-16 on every case (2.8e-14 before, on the ordinary cases). The fingerprint golden moved by 4–335 ulp on its two Kruskal-Wallis entries; the new mtcars statistic is the correctly rounded exact value (the old one was 4 ulp off). The golden pins the deviation form and keeps `kruskal.test`'s own figures as `r_statistic` / `r_p_value` (the U08 refinement pattern), asserted equal to 1e-11 on every other case.
    - **Definitional differences, pinned as Pulse defines them** (R made to compute Pulse's definition, R's default kept beside):
      - `TEST_CHISQ` applies no Yates correction: `chisq.test(correct = FALSE)`; the corrected 2 × 2 figure is kept as `r_yates_*`.
      - `TEST_MANN_WHITNEY_U` has no exact arm: `wilcox.test(exact = FALSE, correct = TRUE)`. The R default's exact p on small untied samples is kept as `r_p_exact`, and the registry row states the gap.
      - `TEST_PROP_Z`'s one-proportion interval is the Wald interval on p − p0, not `prop.test`'s Wilson interval on p (kept as `r_wilson_*`). The two-proportion Wald interval equals `prop.test`'s; `prop.test` clips it to [−1, 1] and Pulse does not, so no case reaches the clip (the generator stops if one would).
      - `TEST_T` has no pooled-variance two-sample form: its two-sample arm is Welch, so every two-sample case runs as both `TEST_T` and `TEST_WELCH` against `t.test(var.equal = FALSE)`.
    - **Skills corrected on the way.** `op-test-welch` and `op-test-z-two-sample` described a `Details.per_group` block and a "pooled SE" that the code does not have; they now state the unpooled (Welch) SE, the flat per-arm `n` / `mean` / `variance` and the pooled-SD `cohens_d`. `op-test-mann-whitney-u` now says the approximation is continuity-corrected.
  - S2 *(done)*: #617.
    - **Overlays.** `scripts/reference/gen_overlay_chisq.R` writes `overlay_chisq_axes.json`, seven count hosts (2 × 2 to 5 × 2, a zero cell, low expected counts, p down to 1e-26). `OVERLAY_CHISQ_MATRIX` is pinned to `chisq.test(table, correct = FALSE)` (no Yates; the corrected 2 × 2 figure is kept as `r_yates_*`), and `OVERLAY_CHISQ_ROW` / `_COL` to each row's (column's) `chisq.test(x, p = colSums / N)` (`rowSums / N`), the goodness of fit to the other margin's distribution. `TestReferenceOracle_OverlayChiSqAxes` runs the three kinds through `applyOverlays`, the dispatch a crosstab request's `overlays[]` takes, and checks the statistic, df, p, the matrix scalar and that `PULSE_OVERLAY_EXPECTED_LOW` fires exactly where R's smallest expected count is below 5. Worst relative error 1.5e-14. Falsified on each handler's df and expected count. No engine change.
    - **`TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK`: a pinned refusal.** Both were already refused permanently under any weight (`PULSE_WEIGHT_UNSUPPORTED` with a stated `details.reason`, `weighting.refusalReasons`); the gap was only that the validation page said "No weighted reference." `TestWeight_UnweightedOnlyTestsRefusalPinned` (`internal/integration/weight_refusal_pin_test.go`) pins it for both kinds and every source (instance default, request, slot), with predict giving the same entry and `weight: null` still running unweighted; it fails when the class table lets either test run. The registry gains a `WeightRefused` field: its test files are gated like a reference's, it renders a "Weighted analysis: refused …" line with the reason taken from the engine (`descx.OperatorWeightRefusal` over `weighting.RefusalReason`, so it cannot drift), and it drops when weighting is hidden. `TestValidationWeightRefusalIsPermanent` fails the claim once either test gains a weighted form; `TestValidationWeightRefusalRenders` pins the rendering both ways.
- **E3 — NIST StRD certification**
  - S1: vendor the StRD data files and certified values under `internal/processing/testdata/strd/`, frozen with hash footers like the other goldens.
  - S2: tests for `REG_OLS` (coefficients, standard errors, residual SD, R²), `TEST_ANOVA_F` (between and within SS, df, MS, F, R², residual SD) and the univariate aggregators (mean, SD, lag-1 autocorrelation). Each test computes LRE = −log10(|x − c| / |c|) and asserts a per-dataset floor recorded in the fixture.
  - S3: docgen renders an "NIST StRD results" table (dataset, difficulty, digits achieved) on `validation.md`, from the same fixture, so the published numbers cannot drift from the tests.
  - **Findings (first run, 2026-10-31):**
    - **Regression.** Norris, Pontius and Longley reach 8.5–11.5 digits, and Wampler1–5 reach 6.0–6.7. Filip is refused as rank-deficient, and NoInt1/2 cannot run because there is no no-intercept option.
    - **ANOVA.** SiRstv, SmLs01–06 and AtmWtAg reach 8.7–14.7 digits; SmLs07–09 reach only 3.2.
    - **Univariate.** The mean and sample SD reach 8.2–15 digits. The sample SD came from `AGG_WELFORD`, since `AGG_STDDEV` was then the population SD only (it now takes `ddof`, below). The lag-1 autocorrelation is not offered.
    - **Follow-ups:** #633, #634 and #635 (S4–S6).
  - S4 *(done)*: #633.
    - **The 7-digit target is impossible.** SmLs07–09 hold values such as 1000000000000.4, which float64 cannot represent (the spacing near 1e12 is 1.2e-4). Exact rational arithmetic on the parsed doubles reaches only 4.03, 3.92 and 3.91 digits. R, Stata and SPSS read the same doubles, so no double-precision package can do better.
    - **The arithmetic loss is fixed.** Pulse lost a further 0.8 digits because each group's Welford mean was held at magnitude 1e12. `TEST_ANOVA_F` now accumulates v minus the first observed value, which is exact and shift-invariant for the sums of squares. It reaches the ceiling (4.0 / 3.9 / 3.9). Every other ANOVA set gained 0.5–1.4 digits; for example, AtmWtAg went from 8.7 to 10.1.
    - **Pinned by** `TestAnovaF_LargeOffsetMatchesExact`, which compares against exact `math/big` sums of squares on the input doubles.
    - **Exact results would need exact input.** Getting past the ceiling would need decimal-aware accumulation on `decimal128` cohorts. That is out of scope.
  - S5 *(done)*: #634.
    - **The change.** Plain `REG_OLS` (no penalty, no bounds) now solves on an incremental Givens QR of √w·[1, x − x⁰, y − y⁰], where the origin is the first row. It is still streaming, at O(p²) per row and O(p²) memory. The solve works at κ(X) instead of the normal equations' κ(X)², and the origin keeps an offset predictor from aligning with the intercept column. The intercept and its SE still come from the Welford means.
    - **R².** It is computed as ESS / (ESS + RSS), as R's `summary.lm` does, so there is no cancellation when R² is near 0.
    - **Unchanged solvers.** Ridge, lasso, NNLS's bounded solve and Bayes keep reading the moments. Every caller of `solveOLS` (refit, coefficient scoring, NNLS's first pass) moves together.
    - **Checks.** The `lm` goldens still pass at 1e-10, and `TestOLSQR_NoFMAContraction` holds `ols_qr.go` FMA-free.
    - **NIST, before → after (digits):**

      | Dataset | Before | After |
      |---|---|---|
      | Filip | refused | 7.4 |
      | Longley | 11.5 | 13.3 |
      | Norris | 10.7 | 12.4 |
      | Pontius | 8.5 | 11.8 |
      | Wampler1 | 6.7 | 9.6 |
      | Wampler2 | 6.4 | 13.3 |
      | Wampler3 | 6.3 | 9.7 |
      | Wampler4 | 6.7 | 7.7 |
      | Wampler5 | 6.0 | 5.7 |
    - **Reference points.** numpy/LAPACK Householder QR gets 8.1 on Filip, 7.6 on Wampler4 and 5.6 on Wampler5. Its SVD-based `lstsq` misses Filip entirely, at 0 digits.
  - S6 *(done)*: #635. `REG_OLS` gained `intercept: false`, which pins the intercept to 0 and gives null inference on it, uncentred R² and df N* − p, as in R's `lm(y ~ 0 + x)`. It also gained `solver` (`qr` default, `normal`). NIST NoInt1 / NoInt2: 14.1 / 15 digits.
  - S7: #636. Each one regenerates `lre.json` with `-update` in the PR that changes the digits, so the review sees the before and after.
  - *(done)* **`ddof` on `AGG_STDDEV` / `AGG_VARIANCE`.** Both gained an optional `params.ddof`: `0` (the default, the population figure m2 / n, byte-identical when absent) or `1` (the sample figure m2 / (n − 1); weighted, `AGG_WELFORD`'s m2_w / (Σw − 1)). Any other value is `PROCESSING_CONFIG`, raised identically by predict and the runtime (`internal/ddofspec`). Every path honours it: buffered, streaming, merged shards, decimal128, weighted, crosstab cells and margins, and the components (`variance` / `stddev` equal the scalar; `m2` and `n` stay raw). One row (or Σw ≤ 1) under ddof 1 is `null`, never 0. The univariate StRD sample SD now comes from `AGG_STDDEV` with `ddof: 1`, with `AGG_WELFORD`'s `stddev` component held to the same digits; the recorded digits did not change, and the registry gains an `AGG_STDDEV` row.
- **E4 — External references for MMM**
  - S1: `gen_mmm_hill.R` fits the same Hill-adstock objective with `minpack.lm::nlsLM` from the same start and bounds. The test pins coefficients at the tolerance the optimizer oracles already use (`optimize/testdata`).
  - S1 *(done)*: #619.
    - **The script.** `scripts/reference/gen_mmm_hill.R` (`minpack.lm` 1.2-4 + `jsonlite`, sourced last by `gen_reference.R`, runnable on its own) writes `mmm_hill.json`: one channel; two channels and a price control; the same rows under a MAP prior on one channel's β; and a channel whose unconstrained β is negative, held at 0. Each fits REG_MMM_HILL's objective (½·Σr² plus the prior's pseudo-residual) with `nlsLM` from the operator's own start (k at the channel mean, s = 1, the linear part by least squares there) inside its bounds. `nls.lm` on the analytic Jacobian must agree to 1e-7, and the stored point is that fit Gauss-Newton polished to the stationary point (gradient 0 to 1e-9). The held case is the reduced problem, and its KKT sign is checked over the channel's whole (k, s) box, because the held channel's k and s are not identified.
    - **The test.** `TestReferenceOracle_MMMHill` (`internal/regmmm/hill_oracle_test.go`) fits every case through `Process` and checks the start (at 1e-11), every coefficient, standard error, R², residual SE, df and deviance, plus a held β of exactly 0 with null standard errors on β, k and s. Falsified on the σ² df, the prior's precision and the s Jacobian.
    - **Worst relative error:** 4.3e-8 on a coefficient and 9.2e-8 on a standard error (both on the held case), 2e-9 on the fit figures, 9.9e-14 on the start. No engine change.
  - S2: `REG_MMM_BAYES` with flat saturation and fixed adstock must reproduce the `REG_BAYES_LINEAR` posterior. A seeded simulation-based calibration run (rank histograms, fixed draw count) becomes a heavy-tier test whose output is published only with its harness, following U63's rule that unreproducible figures are not published.
  - S2 *(done)*: #620.
    - **Conjugate limit.** `TestMMMBayes_ConjugateLimitMatchesBayesLinear` (`internal/regmmm/bayes_conjugate_heavy_test.go`, heavy, about 5 s) fits the small one-channel cohort (60 weeks, a price control) in one request, twice: REG_MMM_BAYES and REG_BAYES_LINEAR. On the REG_MMM_BAYES side, the decay, k and s are pinned (each a truncnormal 1e-9 relative wide), β is under HalfNormal(100), the intercept and control under Normal(0, 100) and σ under HalfNormal(100), all on the scaled outcome; the run is 4 chains × 2500 draws, seeded. On the REG_BAYES_LINEAR side, `prior_precision` is 0 on every coefficient and `prior_shape` = `prior_rate` = 1e-12, on the design [1, price, h], with h the pinned curve computed by the test. The means agree. The SDs agree after the known factor √(DF / (ν − 2)), ν = n − P − 1: the MMM model's flat prior is on σ, and NIG's flat limit has a σ^−p factor. E[σ] and SD[σ] come from σ² ~ InvGamma(ν/2, SSR/2). Worst gap: 1.39 Monte Carlo SEs (bound 4). Falsified twice. With the likelihood's n·log σ off by 2, σ misses by 13 SEs. With a 0.2% error in β's back-transform, β misses by 9.5 SEs.
    - **SBC.** `TestMMMBayes_SimulationBasedCalibration` (`internal/regmmm/bayes_sbc_heavy_test.go`, heavy, about 20 s) runs Talts et al.'s calibration on the default priors (ROI mode): 200 replicates over a fixed 52-week design (one channel, one control, `max_lag` 4). Each draws a truth from the priors, simulates the likelihood, fits it with MAP and then NUTS (1 chain, 400 warmup, 495 draws thinned by 5), and ranks the truth among 99 draws. Every replicate is seeded (PCG and `detmath` variates, sampler seed per replicate), so the histograms are identical across runs and worker counts. Each parameter's histogram passes a χ²(9) uniformity test at 0.001 / 7. The committed seed gives p = 0.82 (intercept), 0.38 (control), 0.71 (decay), 0.76 (k), 0.32 (s), 0.94 (ROI) and 0.92 (σ), with 38 post-warmup divergences across the 200 runs. Falsified: dropping the transforms' log-Jacobians fails five of the seven parameters (p < 1e-3).
    - **No engine change.** The registry row now cites both tests. Its gap states what neither covers: no external MCMC implementation is compared on the full nonlinear model, and the outcome scale is fixed.
- **E5 — Studio** (pulse-studio, after the Pulse tag carrying E1-S2)
  - S1: the analysis inspector shows a "Validated against" disclosure for the selected operator node, from the engine surface, linking the book page. The palette tooltip shows a short badge: "R-pinned", "weighted only" or "no external reference".
  - S2: Studio's own arithmetic gets its own table, in the README and the inspector:
    - the Stat test node (`two_proportion`, `welch_t`, `chi_square`), whose R 4.6.1 goldens are already inline in `internal/workflow/stattest_test.go`;
    - the Fit statistics node;
    - any other Studio-computed figure the inventory finds.

## Acceptance criteria

- Every built-in test, regression, matrix operator and inferential overlay has a validation row. Every row cites test files that exist and that exercise the operator.
- After E2 no row in the "Statistical tests" table has a tolerance looser than 1e-9 relative, and every remaining gap is a deliberate, worded one.
- The NIST table is generated from the same fixture the tests assert, and states the digits achieved per dataset.
- Studio shows each operator's validation without a separate source of truth.

## Gates & tests

- New: the E1 gates above (the registry gates in `internal/descriptor`, the rendering ones in `internal/docgen`), `TestValidationExercisesRule`, `TestValidation_ScrubsHiddenNames` and `TestExplainOperator_*`. None of them uses a CLAUDE.md-listed prefix, so they need no entry in the Non-Skippable CI Gates list. The StRD LRE tests use the existing `TestGoldensNotHandEdited` hash footer.
- Existing: `TestDocsGeneratedCurrent` (regenerate with `make docs`), `TestGoldensNotHandEdited`, the `TestReferenceOracle_*` tests.

## Update Demand companions

- `docs/src/internals/regenerating-goldens.md` (StRD fixture, the new R scripts), `.claude/reference/guided-analysis/explain-and-skills.md` (Validation page; the engine surface after D1)
- If D1 picks (b): the payload-schema and manifest goldens, `TestManifestGuidanceBudget`, and the guided-analysis Update Demand row
- `make docs` after every registry change. Never hand-edit `docs/src/guide`.
- Studio: README "Running and results" / inspector docs; `make bump PULSE_REF=<tag>` then `make catalog-fixture`

## Handed on to later work

- Regression diagnostics, links and new models: [U65](U65-regression-depth.md). Each output it adds arrives with its R golden and registry row.
