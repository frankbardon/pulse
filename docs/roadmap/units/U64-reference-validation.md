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
- `internal/docgen/validation.go` + `validation_intro.md`; `.claude/reference/guided-analysis/explain-and-skills.md` (Validation page)
- [U08 statistics review](../reviews/U08-statistics-review.md) (the R-oracle layer); [U61](U61-cross-arch-determinism.md) (identical bits across architectures)
- NIST Statistical Reference Datasets: <https://www.itl.nist.gov/div898/strd/>

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#613** The book's "How Pulse is validated" page and a "Validated against" list on every inferential operator's catalog entry, generated from a gated registry (E1-S1)
- [ ] **#614** Validation evidence reachable from the engine (pulled, never pushed), so Studio and agents can show it (E1-S2)
- [ ] **#615** The registry gate checks that a cited test actually exercises the operator, not only that the file exists (E1-S3)
- [x] **#616** R goldens at 1e-10 for the unweighted `TEST_T`, `TEST_WELCH`, `TEST_Z_TWO_SAMPLE`, `TEST_CHISQ`, `TEST_MANN_WHITNEY_U`, `TEST_KRUSKAL_WALLIS`, `TEST_PROP_Z` (E2-S1)
- [x] **#617** R goldens for the unweighted `OVERLAY_CHISQ_ROW` / `_COL` / `_MATRIX`; weighted goldens or a pinned refusal for `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK` (E2-S2)
- [x] **#618** NIST StRD linear regression (all 11 datasets, Norris to Filip and Longley), one-way ANOVA (all 11) and univariate summary statistics (all 9) as goldens, with the certified log relative error (LRE) per dataset published (E3)
- [x] **#633** `TEST_ANOVA_F` reaches the double-precision ceiling on NIST SmLs07–09: 4.0 / 3.9 / 3.9 digits (3.2 before). The original 7-digit target is impossible from float64 input; see E3-S4 (E3-S4)
- [x] **#636** Shifted accumulation for the other per-group Welford tests where no parity contract forbids it: `TEST_ANOVA_WELCH` and one-sample `TEST_T`. The rest don't need it: `TEST_BROWN_FORSYTHE` reads deviations, the paired tests read differences, and `TEST_TUKEY_HSD` and the post-tests read materialized means (E3-S7)
- [ ] **#637** Decide, then do: two-sample `TEST_T` / `TEST_WELCH` / `TEST_Z_TWO_SAMPLE` must stay byte-equal with `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` / `_VS_REF`, which fold unshifted `AGG_WELFORD` cells. They lose about 3 digits under a 1e12 offset (1e-9 relative at 1e6). Precision there means shifting `AGG_WELFORD` itself, merge across shards included, so both sides move together (owner decision D4)
- [x] **#634** `REG_OLS` solves on a streaming Givens QR (`ols_qr.go`, AS 274) instead of Cholesky on the normal equations. NIST Filip now fits (7.4 digits; it was refused), and Wampler1–5 reach 9.6 / 13.3 / 9.7 / 7.7 / 5.7 (6.0–6.7 before), level with LAPACK's Householder QR (8.1, and 7.6 / 5.6 on Wampler4 / 5). Wampler5's 7-digit target is beyond double precision for any solver (E3-S5)
- [x] **#635** Regression through the origin, `intercept: false` on `REG_OLS` (default `true`). NIST NoInt1 / NoInt2 reach 14.1 / 15 digits. `solver: "qr" | "normal"` (default `qr`) restores the normal-equations path on request. Both are refused by the shared rule `internal/regsolve`, at predict and at run (E3-S6)
- [ ] **#644** Decide, then do: the methodological options the E2-S1 pins surfaced, each a candidate knob under the configurability rule with today's behaviour as its default — a pooled-variance (Student) two-sample `TEST_T`, an exact `TEST_MANN_WHITNEY_U` p, Yates' correction on a 2 × 2 `TEST_CHISQ`, and the Wilson interval for the one-proportion `TEST_PROP_Z` (E2-S1 follow-up)
- [ ] **#619** `REG_MMM_HILL` pinned to R `minpack.lm::nlsLM` on the same objective and starting values (E4-S1)
- [ ] **#620** `REG_MMM_BAYES` pinned in its conjugate limit to `REG_BAYES_LINEAR`, plus a seeded, reproducible simulation-based calibration harness (E4-S2)
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

- **E2-S1 script placement.** The unweighted pins live in a new standalone script, `gen_unweighted_tests.R`, not in `gen_test_outputs.R` as S1 sketched. `gen_test_outputs.R` cannot run alone (it needs `gen_reference.R`'s quadrature and the `Kendall` and `nortest` pins), and regenerating it would rewrite nine unrelated goldens. The new script needs base R only and moves no other golden.
- **E2-S1 "both variances".** Pulse offers no pooled-variance two-sample t, so `t.test` is pinned on its one-sample and Welch forms. Adding `var_equal` (and the other options the pins surfaced) is a new methodological knob, not a validation fix; it is #644, for the owner.
- **E2-S1 near-null Kruskal-Wallis.** Where stock R loses digits to cancellation the golden pins the exact deviation-form value and keeps R's own beside it, the pattern Fisher and Tukey already use. The engine was fixed to match rather than the tolerance loosened.

- **E2-S2: refusal over a weighted golden for `TEST_ANOVA_RM` and `TEST_SHAPIRO_WILK`.** A `rep()` expansion is not a meaningful weighted repeated-measures ANOVA: copies of a subject-by-condition row become within-cell replicates, which changes the model `aov(Error())` fits. A frequency-weighted Shapiro-Francia W′ is computable on expanded rows, but its p would come from Royston's calibration applied to a tie-heavy sample it was not built for, and no probability-weighted form exists. Both refusals were already permanent and stated; this story pins them and publishes them. Lifting either is new weighted methodology, not validation.
- **E2-S2: a `WeightRefused` registry field, not `Gap` prose.** A refusal named in `Gap` text would cite a test file no gate checks, and would show on instances that hide weighting. The field is gated, takes its wording from the engine's own refusal reason and follows the weighting feature. `TEST_TREND` and `TEST_TUKEY_HSD` (post-test only) state their refusal too, on owner request: every weighted post-test is refused with the shared post-test reason (the result rows carry no row weights), not the operator's own `RefusalReason`, so their records set `PostTestOnly` and quote `descx.PostTestWeightRefusal`. The pin covers them; their opt-out run (Tukey over every subject group, seconds) stays with the heavy `TestWeight_InferentialRefusalsMatchPredict`, and the fast pin checks the opt-out in predict.

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
    - Gates: `TestValidationRegistryCoversInferential`, `TestValidationRegistryNamesBuiltins`, `TestValidationReferencesExist`, `TestValidationDropsWeightedWhenHidden`.
  - S2: the engine surface chosen in D1. If it is (a), `pulse explain OP` and `pulse_explain` carry the operator's references and gap. The registry moves to `internal/descriptor` so docgen and the surface share it.
  - S3: the gate checks that each cited test file names the operator, or the golden file that does.
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
  - S2: `REG_MMM_BAYES` with flat saturation and fixed adstock must reproduce the `REG_BAYES_LINEAR` posterior. A seeded simulation-based calibration run (rank histograms, fixed draw count) becomes a heavy-tier test whose output is published only with its harness, following U63's rule that unreproducible figures are not published.
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

- New: the E1 gates above (in `internal/docgen`). None of them uses a CLAUDE.md-listed prefix, so they need no entry in the Non-Skippable CI Gates list. The StRD LRE tests use the existing `TestGoldensNotHandEdited` hash footer.
- Existing: `TestDocsGeneratedCurrent` (regenerate with `make docs`), `TestGoldensNotHandEdited`, the `TestReferenceOracle_*` tests.

## Update Demand companions

- `docs/src/internals/regenerating-goldens.md` (StRD fixture, the new R scripts), `.claude/reference/guided-analysis/explain-and-skills.md` (Validation page; the engine surface after D1)
- If D1 picks (b): the payload-schema and manifest goldens, `TestManifestGuidanceBudget`, and the guided-analysis Update Demand row
- `make docs` after every registry change. Never hand-edit `docs/src/guide`.
- Studio: README "Running and results" / inspector docs; `make bump PULSE_REF=<tag>` then `make catalog-fixture`

## Handed on to later work

- Regression diagnostics, links and new models: [U65](U65-regression-depth.md). Each output it adds arrives with its R golden and registry row.
