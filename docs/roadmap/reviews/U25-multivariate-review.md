# U25 multivariate tests, scoring, segmentation and robust covariance review

The committed record of the statistical review of the operators and options roadmap [U25](../units/U25-multivariate-tests-segmentation.md) shipped. It is an input for the human statistics reviewer at [U33](../units/U33-v1-release.md) (#206), beside the [U08](U08-statistics-review.md), [U12](U12-weighting-review.md) and [U24](U24-matrix-operators-review.md) records.

- **Scope:** `TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BARTLETT_SPHERICITY`, `TEST_BOX_M`, `TEST_MARDIA`; `MAT_FACTOR` and the rotation engine (also behind `MAT_PCA` `params.rotation`); `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`; `GROUP_KMEANS`; `MAT_DISTANCE`; `RegressionSpec.Covariance` on `REG_OLS`, `REG_GLM` and `REG_BAYES_LINEAR`.
- **Not in scope:** the co-moment, Cholesky and PSD guard (U16, U24 records); the weight rule itself (U12 record); row-to-row distances (U27).
- **Status:** done without a human reviewer. The deterministic gates below bind in CI. The advisory review is one automated reading of the code, the skills and the owner-review notes collected while the unit was built. **Human sign-off is deferred to U33 (#206).**

## Method

### Deterministic layers (binding CI gates)

1. **External R oracle.** `scripts/reference/gen_multivariate.R` (and its `gen_factor.R`, `gen_robust_vcov.R` and `gen_robust_vcov_glm.R` siblings, run by `make reference`, never by CI) writes `internal/processing/testdata/reference/mv_*.json`. The committed values are the oracle; the headers pin the package versions.
2. **Weights follow the one rule** (`.claude/reference/weighting.md`): N* = Σw under `frequency`, Kish n_eff under `probability`, every formula the frequency formula on w*. Frequency cases are asserted equal to the `rep()`-expanded rows in R. Operators that need integer class counts refuse a probability weight (`TEST_MARDIA`, `GROUP_KMEANS`).
3. **Predict = runtime, streaming = buffered** parity suites, extended to every new slot; streamable tests (Hotelling, MANOVA, Box's M, Bartlett) are bit-equal to their buffered path, `GROUP_KMEANS` is invariant under row order, shard order and decode workers on both arms.
4. **Coverage and guidance gates:** `TestSkillsCoverAll*`, `TestOperatorHasAtomicSkill`, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes` and `TestInterpretationCoversOutputs`. Every new operator carries a Purpose, every inferential output an Interpretation, and the `measure_construct` and `factor` ledger entries are closed.

### Oracle files and tolerances

Tolerance is absolute-plus-relative unless stated. The constant is the one the consuming test declares.

| Oracle file | Reference (R) | Pins | Tolerance |
|---|---|---|---|
| `mv_hotelling.json` | closed form `n·d'S⁻¹d` (`anova.mlm` asserted); `Hotelling::hotelling.test(var.equal = TRUE)`; paired = one-sample on the difference; unequal-covariance branch hand-computed (no package implements it, cross-checked three ways) | T², F, df, p, one-sample / two-sample / paired / unequal, none / frequency / probability weight (36 cases) | 1e-10 (`hotellingOracleTol`); N* 1e-12 |
| `mv_manova.json` | `stats:::Pillai / Wilks / HL / Roy` on `eigen(E⁻¹H)`; `summary(manova(), test =)` | all four statistics, F, df, p; two levels equal Hotelling | 1e-10 (`manovaOracleTol`); the two-level identity 1e-11 (F) and 1e-9 (p) |
| `mv_box_m.json` | `biotools::boxM` (χ²); the F form hand-computed in SPSS's form and checked against SPSS's iris figures | M, χ², df, F, df1, df2, both F branches | 1e-10 (`boxMOracleTol`); F p-value 1e-9 |
| `mv_mardia.json` | closed form of `MVN::mvn(mvnTest = "mardia")`, asserted against `psych::mardia` (MVN does not install on R 4.6.1) | b1p, b2p, χ², z, p, the small-sample correction | 1e-10 (`mardiaOracleTol`) |
| `mv_rotation.json` | `stats::varimax`, `stats::promax(m =)`, `GPArotation::oblimin(gam =)` (`normalize = FALSE`, bb, eps 1e-5) | rotated loadings, rotation matrix, column order and signs exact | 1e-9 (`rotationOracleTol`) |
| `mv_fa_minres.json` | `psych::fa(nfactors = 1, fm = "minres")` | one-factor loadings, uniquenesses, Heywood flag | 1e-5 loadings (psych's L-BFGS-B stopping error; Pulse's residual is never above psych's) |
| `mv_factor.json` | `psych::fa(fm = "minres" / "pa" / "ml")`, `psych::fa.stats`, then `varimax` / `promax(m = 4)` / `oblimin(gam = 0)` on the unrotated loadings | loadings, uniquenesses, communalities, ML likelihood χ², p, TLI, RMSEA, BIC | 1e-9 principal axis (`faPAOracleTol`) and ML fit indices (`faMLStatsTol`), 1e-7 exact minres (`faExactOracleTol`), 1e-4 ML against psych's early-stopped optimum (`faMLPsychTol`); oblimin over minres 1e-4 |
| `mv_factor_score.json` | `psych::factor.scores(R, f, Phi, method = "Thurstone" / "Bartlett")$weights` | regression and Bartlett scores, weighted | 1e-8, 1e-6 where the minres loadings enter |
| `mv_mahalanobis.json` | `stats::mahalanobis`, `pchisq`; weighted = `rep()`-expanded fit rows | D², p, fit on a `fit_on` subset | 1e-10 relative, 1e-12 absolute floor |
| `mv_pc_score.json` | `prcomp` / `psych::principal(rotate = "varimax")`, `eigen` of `cov2cor(S)` | standardized component scores, rotated and unrotated, weighted (24 cases) | 1e-9 (`pcScoreOracleTol`) |
| `mv_kmeans.json` | `stats::kmeans(algorithm = "Lloyd")` from fixed starts on separable blobs; best `tot.withinss` of `nstart = 200` on `attitude` | centroids, labels, within-SS; the Lloyd fixed point | 1e-9 relative; within-SS no worse than R's best at `n_init = 200` |
| `mv_distance.json` | `weighted.mean` centroids, `stats::dist` (euclidean, manhattan, maximum), squared, cosine, `sqrt(mahalanobis())` over the pooled within-bucket covariance | k × k distance matrix, `standardize`, weighted | 1e-9 relative |
| `mv_robust_vcov.json` | `sandwich::vcovHC` (HC0–HC3), `vcovCL` (CR1), `NeweyWest(prewhite = FALSE, adjust = TRUE)`, `lmtest::coeftest` | `REG_OLS` standard errors, p, intervals, vcov, weighted | 1e-10 (`robustTol`) |
| `mv_robust_vcov_glm.json` | `sandwich` on `glm` (binomial, poisson), a Müller sandwich posterior for `REG_BAYES_LINEAR` | GLM and Bayes robust covariance, cluster Wald z | 1e-10 (`robustGLMBayesTol`; 1e-11 on arm64, room for amd64) |

Closed-form weight references (`scripts/reference/gen_weight_reference.py`, tolerance 1e-10, widened to any key ending `p_value`) cover the weighted tests; `MAT_DISTANCE` has no row there because the harness has no matrix surface (the `MAT_FACTOR` precedent), so its weighted coverage rides the R oracle.

## Advisory review: Purpose and Interpretation

The pass read each new operator's `Purpose` (plain statement, questions, assumptions, "use instead" pointers) and `Interpretation` against its skill and its `Details` keys, looking for an overclaim, a missing caveat or a wrong direction. Findings:

- **Tests.** Assumptions state listwise deletion, independence, and more complete rows than fields. `TEST_MARDIA` says that with many rows trivial departures are flagged and with few real ones are missed, and points to `TEST_BOX_M` for the shared-covariance question. Box's M is known to over-reject on mild non-normality, so its Interpretation should be read as an assumption flag, not a verdict. **No change needed.**
- **`ATTR_MAHALANOBIS`.** Reads D² as a distance from the fit's centre, with p from a χ² on p degrees of freedom. A p per row is n looks; nothing is corrected (the multiplicity core does not reach row-level attributes). **Acceptable; the wording could name that explicitly.**
- **`ATTR_PC_SCORE` / `ATTR_FACTOR_SCORE`.** Assumptions: one fit over the filtered rows (or the `fit_on` subset), never per group; every filtered row is scored; a component's direction follows `MAT_PCA`'s rule and is not a meaning; every score is empty, with a warning, when the fit is singular or the factor count is not identified. **Acceptable.**
- **`MAT_FACTOR`.** Loadings are not unique up to rotation; a Heywood case (uniqueness at or below 0) is flagged, not repaired. **Acceptable.**
- **`GROUP_KMEANS` / `MAT_DISTANCE`.** Segments are found, not given; labels `cluster_1`…`cluster_k` run largest to smallest; the grouper cannot stream; a row with a missing number lands in no segment. Distances are between centres, in the metric's units. **Acceptable.**
- **Robust covariance.** The `regression-inference` skill states which assumption each type relaxes and that the coefficients do not move. The MMM worked example shows a robust standard error can go down as well as up. **Acceptable.**

## Advisory review: formula choices

### MANOVA F approximations

Pillai's V is the headline because it is the most robust of the four to unequal covariances. All four statistics are read from the eigenvalues λ of E⁻¹H with R's `summary.manova` approximations: Pillai V = Σ λ/(1+λ), Wilks Λ = Π 1/(1+λ) (Rao's F), Hotelling–Lawley U = Σ λ, Roy θ = max λ. All match `summary(manova())` at 1e-10. **Roy's F is an upper bound, so its p is a lower bound** (`roy.upper_bound: true`; R's behaviour). A statistic's p is null when its F is negative or a df is not positive. With two groups all four share one exact F and equal pooled Hotelling.

H is computed from N*-weighted level means (not from merged clones, as the story first said), and E + H equals the total SSCP (checked against R). A **singular within-level SSCP** gives null figures and `PULSE_MATRIX_SINGULAR`, and the test needs `n ≥ p + k` complete rows.

### Hotelling: Krishnamoorthy-Yu (modified Nel-Van der Merwe)

Pooled two-sample, one-sample and paired use the exact F. For `params.variance: unequal` the test is Krishnamoorthy and Yu (2004): T² = dᵀS̃⁻¹d, ν = (p + p²) / Σᵢ (tr[(VᵢS̃⁻¹)²] + tr[VᵢS̃⁻¹]²)/(N*ᵢ − 1), F = (ν − p + 1)/(νp)·T² on (p, ν − p + 1). It is the Nel-Van der Merwe ν with a small-sample F; at p = 1 it is Welch's t. **No R package implements it**; the oracle is a hand-computed R implementation cross-checked three ways. A reviewer should check the ν expression, not the numerics. Paired is the one-sample test on the difference vector, so the two agree bit-for-bit. The closed-form weight reference does not cover paired or unequal (the R oracle and parity rows do).

### Box's M

With νg = N*g − 1, M = Σν·ln|Sp| − Σ νg ln|Sg| and χ² = M(1 − A1) on p(p+1)/2·(k−1) df, A1 = (2p² + 3p − 1)/(6(p+1)(k−1))·(Σ 1/νg − 1/Σν), term for term as `biotools::boxM`. The F approximation (in `Details`) uses A2 and takes SPSS's form on either side of A2 = A1²; A2 = A1² leaves F null. It is checked against SPSS's iris figures, and the F p-value to 1e-9 (the scipy tail is ~3e-11 off). A singular per-level or pooled covariance gives null figures and a warning.

### Mardia

Skewness χ² = N·b1p/6 on p(p+1)(p+2)/6 df, times the small-sample factor (p+1)(N+1)(N+3)/(N((N+1)(p+1) − 6)) when N < 20; kurtosis z = (b2p − p(p+2))/√(8p(p+2)/N), two-sided. b1p is formed from a third-moment tensor in O(n·p³), never the n × n matrix. These are `MVN::mvn(mvnTest = "mardia")`'s figures. The oracle is the closed form in base R, asserted against `psych::mardia` after rescaling (MVN does not install on R 4.6.1). Only a frequency weight is accepted (the statistics are functions of integer counts); a probability weight is refused. The test is buffered (`Streamable` false; predict says so).

### Factor analysis: extraction and rotation

- **Extraction.** `minres` (default), `principal_axis`, `ml`. Minres is a Newton solve (stops at `max|d| ≤ 1e-10`), exact against `psych` to 1e-5 (psych's own optimiser stops earlier). Principal axis is the same fixed point psych iterates. ML is a projected Newton with the exact Hessian: 6e-11 from the exact optimum, 2.2e-5 from psych's early-stopped one.
- **Rotation.** Varimax (Kaiser normalized, eps 1e-5), promax (`m = 4`, via the normal equations rather than `lm.fit`'s QR, well inside 1e-9) and GPA oblimin, sharing one engine with `MAT_PCA` varimax. **Oblimin over minres is held to 1e-4**: GPA stops when the projected gradient falls below 1e-5, so an input that moved by 1e-11 (the gap between the two minres solutions) can stop it one iteration later. This is stated in `matrix-and-vectors.md`. A reviewer can accept it or tighten the GPA stop.
- **Heywood policy (no ψ-clamp).** Pulse emits uniquenesses as solved and raises `PULSE_MATRIX_HEYWOOD` when any ψ ≤ 0; `psych` clamps ψ to [0.005, 1]. The oracle compares **detection** (the flag), not the clamped numbers. This is the PRD's "emit as is" decision: a clamped uniqueness hides a boundary solution the analyst needs to see. The cost is that downstream consumers (Bartlett scores) must handle ψ ≤ 0 themselves; they do (below).
- **Identification.** A factor count the correlation matrix cannot identify is refused with `PULSE_MATRIX_NOT_IDENTIFIED`; `kaiser` keeping fewer factors than a requested `params.factor` leaves the scores unidentified (see below). A non-converged solve warns `PULSE_MATRIX_NOT_CONVERGED`.
- **Fit indices.** ML adds likelihood χ², p, TLI, RMSEA and BIC, matching `psych::fa.stats` to 1e-9. **No RMSEA confidence interval** (#405).

### Factor scores: refusals

`ATTR_FACTOR_SCORE` runs a regression (Thurstone) or Bartlett score on `MAT_FACTOR`'s inline fit. Two refusals are owner-review calls: a **Kaiser rule that keeps fewer factors than the request names nulls every score** with `PULSE_MATRIX_NOT_IDENTIFIED` (the code is reused, the fit is a different model than the one asked for); a **Bartlett score with any uniqueness ≤ 0 nulls every score** with `PULSE_MATRIX_HEYWOOD`, since the weights divide by ψ. Regression scores still score and warn. A refusal drops the fit's not-converged note (#415). Because many weighted oracle fixtures are Heywood cases, the weighted Bartlett scoring path is thinly pinned to `psych` (#410).

### k-means `n_init`

`GROUP_KMEANS` is seeded k-means++ with **`n_init = 10` by default**, size-ordered labels (so they are stable across runs, worker counts and shard orders). On `attitude` (k = 3) ten starts reach a within-SS of 105.28 where R's best of 200 Hartigan–Wong starts is 104.97; `n_init = 200` matches. The default trades fit quality for time. It is the PRD's number. A reviewer may prefer a larger default; raising it changes labels, so it is a breaking change after v1. Only frequency weights are accepted.

### CR1 and HAC small-sample, weighted HAC

- **HC0–HC3.** HC0 is White's sandwich; HC1 scales by `N*/(N* − k)`; HC2 and HC3 weight each squared residual by `1/(1 − h)` and `1/(1 − h)²`.
- **Cluster (CR1).** `G/(G−1) · (N*−1)/(N*−k)`. `REG_OLS` reads it on `G − 1` degrees of freedom (as `lmtest::coeftest` does after `vcovCL`); `REG_GLM` and `REG_BAYES_LINEAR` stay normal (below). With few clusters the sandwich is biased down, and the intervals are wide for that reason: the MMM example gives 8 region clusters, `df` 7.
- **HAC.** Newey–West: Bartlett kernel, truncation `lags` (`auto` = ⌊4(n/100)^(2/9)⌋), no prewhitening, scaled `N*/(N*−k)` (the `NeweyWest(adjust = TRUE)` convention). It reads **one series** and refuses a repeated `order_by` value (`order_by_ties`): a pooled panel cannot use it, so the MMM example clusters on the week (#401 files panel HAC).
- **Weighted HAC (owner-review).** Each row is one period and enters as the score `w*·e·x`, which equals `NeweyWest` on `lm(weights = w*)` times `N*/(N* − k)`. So **weighted HAC lag-0 is not weighted HC0**, whereas HC and cluster use the expanded-row rule (a frequency weight behaves as repeated rows). The two rules agree unweighted. This is documented in `weighting.md`; a reviewer should confirm the series-of-periods reading is the one wanted for a weighted time series.

### Bayes sandwich and GLM cluster Wald z

- **`REG_BAYES_LINEAR`.** Müller's sandwich posterior REPLACES the posterior covariance and keeps the posterior mean. **The bread is the posterior precision Λₙ⁻¹ (the prior included), free of σ².** Robust intervals are **normal with no `df`**. Reviewers should note that a robust "credible interval" is a sandwich-based interval around a posterior mean, not a posterior in the Bayesian sense.
- **`REG_GLM`.** The sandwich runs over the IRLS scores at the converged weights (`sandwich` on `glm`), dispersion-free for `hc0`–`hc3`. **Cluster inference is the Wald z** (`lmtest::coeftest`) rather than the `G − 1` t used for OLS. Gamma standard errors still fix dispersion at 1 while R estimates it (U24 #271).
- `covariance` beside `penalty` (l1 / elasticnet), `resample` or `selection` is refused with `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. `model_format_version` 2 records the covariance in a saved model; version 1 files stay byte-identical when no robust covariance is set.

## Open owner calls (for the U33 reviewer)

1. Oblimin-over-minres loading tolerance 1e-4 (GPA stop 1e-5): accept or tighten.
2. No ψ-clamp (Heywood emitted as is, detection-only oracle): confirm the PRD decision.
3. Kaiser-under-request and Bartlett-with-Heywood null every score: confirm refusal over a degraded score.
4. `GROUP_KMEANS` `n_init = 10` against R's best at 200.
5. Weighted HAC as a series of periods (lag-0 differs from weighted HC0).
6. Bayes robust intervals normal, no `df`; GLM cluster Wald z against OLS `G − 1` t.
7. Roy's p as a lower bound, Krishnamoorthy–Yu oracle without an R package.
8. MAT_PCA is singular only when `InverseSPD` fails, while the tests use `SymEigen` against `RankTolerance` (#408).

Follow-ups filed: [TODO](../TODO.md) #401–#415 under "Follow-ups from U25".
