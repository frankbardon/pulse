# U24 matrix-operators statistics review

The committed record of the statistical review of Pulse's multivariate matrix operators (roadmap [U24](../units/U24-matrix-operators.md)). It is an input for the human statistics reviewer at [U33](../units/U33-v1-release.md) (#206), beside the [U08](U08-statistics-review.md) and [U12](U12-weighting-review.md) records.

- **Scope:** `MAT_CORRELATION` `params.method` `spearman` / `kendall`, `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`, `MAT_PCA` (with KMO and Bartlett), `MAT_COLLINEARITY`, the shared PSD guard with Higham repair, the one-factor minres solver, and the opt-in regression coefficient covariance (`vcov`). Every new `Purpose` and `Interpretation` text.
- **Not in scope:** `MAT_COVARIANCE` and Pearson `MAT_CORRELATION` (U16, pinned by its own reference fixtures); raw p-values and intervals on matrices (U28).

## Method

### Deterministic layers (binding CI gates)

1. **External R oracle.** `scripts/reference/gen_multivariate.R` (run by `make reference`, never by CI) writes `internal/processing/testdata/reference/mv_*.json` and the `.pulse` fixtures; the committed values are the oracle. The script refuses to run on any package version other than the pinned set: R 4.6.1, stats 4.6.1, jsonlite 2.0.0, Matrix 1.7-5, psych 2.6.9, car 3.1-5, ppcor 1.1, perturb 2.10, corpcor 1.6.10. Fixtures: `datasets::attitude`, `datasets::mtcars` (public domain, plus two SEEDED weight columns), and seeded edge cases (`likert_ties`, `nulls`, `weighted`, `nonpsd_pairwise`, `heywood`, `reversed_items`).
2. **Weights follow the one rule** (`.claude/reference/weighting.md`): N* = Σw under `frequency`, Kish n_eff under `probability`, every formula the frequency formula on w*. The oracle builds weighted covariances with `cov.wt` (frequency asserted equal to the `rep()`-expanded rows); rank methods take frequency weights only and are computed on the expansion.
3. **Predict = runtime, unity and expansion parity** are the U12 gates, extended to the matrix slot (`TestMatrix*` weight suites); a probability weight on a rank method is `PULSE_WEIGHT_UNSUPPORTED`.
4. **Determinism.** The reference-kernel path (co-moment, Cholesky, minres, Kendall counts) is FMA-free and bit-identical across architectures; gonum-backed `SymEigen` / SVD (PCA, collinearity) is bitwise on one machine and held to the tolerance below across machines. PCA sign convention: largest-magnitude component positive, a tie within 2^-26 relative to the lowest index.

### Per-output oracle table

Tolerance is absolute-plus-relative unless stated. "Weighted" is the weight kinds the oracle covers (F = frequency, P = probability).

| Operator | Output | R function (package version) | Fixture(s) | Tolerance | Weighted |
|---|---|---|---|---|---|
| `MAT_CORRELATION` spearman | `primary` (rho), pairwise `auxiliary.n` | `cor(method = "spearman", use = "everything" / "pairwise.complete.obs")` (stats 4.6.1) | attitude, mtcars, likert_ties, nulls, weighted | 1e-12 | F (on the `rep()` expansion); P refused |
| `MAT_CORRELATION` kendall | `primary` (tau-b), pairwise `auxiliary.n` | `cor(method = "kendall", ...)` (stats 4.6.1) | attitude, mtcars, likert_ties, nulls, weighted | 1e-12 | F; P refused |
| `MAT_PARTIAL_CORRELATION` `control: "all"` | `primary` | `ppcor::pcor` (1.1); `corpcor::cor2pcor` (1.6.10) on the (weighted) correlation | attitude, mtcars, weighted, nulls (listwise; one pairwise case) | 1e-10 | F, P |
| `MAT_PARTIAL_CORRELATION` `control: [...]` | `primary` | `ppcor::pcor.test(x, y, Z)` (1.1) asserted equal to `cor2pcor` on the sub-block | attitude, mtcars | 1e-10 | F, P (`cor2pcor` on the weighted R; `pcor.test` asserted on unweighted) |
| PSD guard repair (`repair: "nearest"`) | repaired matrix, `frobenius_adjustment`, `iterations`, `converged` | `Matrix::nearPD(corr = TRUE, do2eigen = FALSE)` (Matrix 1.7-5) | nonpsd_pairwise | 1e-9 | unweighted |
| one-factor minres | loadings, uniquenesses, Heywood flag | `psych::fa(nfactors = 1, fm = "minres", rotate = "none")` (2.6.9) | attitude, likert_ties, reversed_items, nulls, heywood | 1e-5 loadings (psych's L-BFGS-B stopping error; Pulse's residual is never above psych's); 1e-10 against the exact start | F, P |
| `MAT_RELIABILITY` | `alpha`, `alpha_standardized`, `mean_inter_item_r`, `item_total_r`, `alpha_if_deleted`, `item_mean`, `item_sd` | `psych::alpha(check.keys = FALSE)` (2.6.9), asserted equal to the manual formulas | attitude, likert_ties, reversed_items, heywood, nulls | 1e-10 | F, P (manual alpha on the weighted covariance) |
| `MAT_RELIABILITY` | `omega` (model form) | `psych::fa(1, fm = "minres")` loadings: (Σλ)² / ((Σλ)² + Σψ) | attitude, likert_ties, reversed_items, nulls | 1e-5 | F, P |
| `MAT_PCA` | `eigenvalues`, `explained_variance`, `cumulative`, `loadings`, `eigenvectors`, `communalities`, Kaiser retention | `eigen(symmetric = TRUE)`; `prcomp` asserted on unweighted listwise (stats 4.6.1) | attitude, mtcars, likert_ties, nulls, weighted | 1e-9 (sign by convention) | F, P |
| `MAT_PCA` | `kmo`, per-member MSA | `psych::KMO(R)` (2.6.9) | same | 1e-9 | F, P |
| `MAT_PCA` | `bartlett_chisq`, `bartlett_df`, `bartlett_p` | `psych::cortest.bartlett(R, n = N*)` (2.6.9) | same | 1e-9 | F, P (N* basis) |
| `MAT_COLLINEARITY` | `vif`, `tolerance`, `max_vif` | `car::vif(lm(...))` (3.1-5), asserted equal to `diag(solve(R))` | mtcars, attitude, weighted | 1e-9 | F, P |
| `MAT_COLLINEARITY` | `condition_indices`, `variance_decomposition` (uncentered with intercept, and centered) | `perturb::colldiag(scale = TRUE, center = FALSE, add.intercept = TRUE)` and `(center = TRUE, add.intercept = FALSE)` (2.10) | mtcars, attitude, weighted | 1e-9, 1e-10 relative | F, P (sqrt(w)-scaled rows) |
| regression `vcov` OLS / ridge | `vcov`, `correlation` | `vcov(lm(...))` (stats 4.6.1) | mtcars, weighted | 1e-10 | F, P |
| regression `vcov` GLM binomial / poisson | `vcov`, `correlation` | `vcov(glm(..., family = ...), control epsilon = 1e-14)` (stats 4.6.1) | mtcars, weighted | 1e-6 (R's covariance uses the weights from one IRLS iteration behind its coefficients); the converged fixed point matches to 1e-10 | F, P |

Gaps with no external oracle, by design or by finding: pairwise weighted PCA / collinearity (the oracle has one unweighted pairwise case each); `MAT_RELIABILITY` probability-weighted pairwise `item_sd`; Bayesian linear `vcov` (a posterior, no R reference); GLM gamma `vcov` (see open owner calls); the pairwise PCA case in `mv_pca.json` is built from `cov2cor(cov(use = "pairwise"))` while Pulse analyses `cor(use = "pairwise")`, so that case is pinned at kernel level and the operator is pinned against its own `MAT_CORRELATION`.

### LLM panel (advisory, maintainer triage pending)

- **Model:** Claude Sonnet 5.5 (the session model), run read-only on 2026-10-09, ONE pass (statistician adversary). It is the model family that wrote the texts, so treat "no finding" as weak evidence. No novice-reader pass: the U08 / U09 binding prose lint covers jargon and length.
- **Texts reviewed:** every `Purpose` in `internal/descriptor/purposes_matrices.go` and every `Interpretation` in `internal/descriptor/interpretations_matrices.go` for the five new or extended operators.
- **Finding rules:** as U08 — every finding quotes the exact span with file and a named source.
- **Triage:** all findings below are OPEN; the docs story changed no guidance string. The reviewer decides which become edits.

#### Pass prompt (statistician)

```text
You are an adversarial statistician reviewing the PURPOSE and INTERPRETATION text of Pulse's multivariate matrix operators (rank and partial correlation, Cronbach's alpha and McDonald's omega, PCA with KMO and Bartlett, VIF and Belsley collinearity diagnostics). Find claims that are statistically wrong, overstated, one-sided, or that would mislead a non-statistician reader. For each: quote the exact span with file, name a source (a textbook, paper, or the R / psych / car documentation), say what is wrong, and propose a fix. Do not comment on style.
```

## Findings

| id | severity | operator | file | quoted span | source | problem | proposed fix | disposition |
|---|---|---|---|---|---|---|---|---|
| MR-01 | medium | `MAT_RELIABILITY` | interpretations_matrices.go (`scalars.alpha`) | "when the ties differ it understates reliability, which omega allows for" | Sijtsma (2009); Green & Yang (2009) | One-sided. Alpha understates reliability under unequal loadings (congeneric items) but OVERSTATES it when item errors correlate (shared wording, method effects). A reader who sees only the first direction will treat a high alpha as safe. | Add the correlated-error direction to the caveat. | open |
| MR-02 | medium | `MAT_PCA` | purposes_matrices.go (Assumptions); interpretations_matrices.go (`primary.values` caveat) | "components summarise shared spread" | Jolliffe (2002) Principal Component Analysis; Fabrigar et al. (1999) | PCA summarises TOTAL variance, including each measure's unique variance. "Shared spread" is the common-factor vocabulary and blurs PCA with factor analysis; the distinction matters once the factor operator ships. | Say "components summarise the measures' total spread". | open |
| MR-03 | low | `MAT_CORRELATION` | interpretations_matrices.go (`primary.values` caveats) | "Kendall's tau-b runs smaller than r or rho for the same strength (about two thirds of rho)" | Kendall (1970) Rank Correlation Methods; Gibbons & Chakraborti (2011) | Under bivariate normality tau = (2/pi) asin(r) and the tau / rho ratio is near 2/3 only for weak to moderate association; it tends to 1 as the association approaches 1. Unsourced and overstated at the top of the scale. | Say "smaller, by about a third for moderate strength", and cite. | open |
| MR-04 | low | `MAT_RELIABILITY` | purposes_matrices.go (KnownAs) | "mcdonald's omega" | McDonald (1999); Revelle & Zinbarg (2009) | Omega names a family (total, hierarchical, categorical). Pulse ships omega-total from a one-factor fit only. | Alias "omega total" or state the form in `Means`. | open |
| MR-05 | low | `MAT_RELIABILITY` | interpretations_matrices.go (`scalars.omega`) | "Null with a warning when the battery has 2 items, ... a Heywood case, or ... an inconsistent pairwise table" | Pulse behaviour (owner call below) | Omits the non-converged fit, where omega is KEPT beside `PULSE_MATRIX_NOT_CONVERGED`. The caveat is incomplete either way the owner decides. | Follow the owner decision on non-converged minres. | open |
| MR-06 | low | `MAT_COLLINEARITY` | interpretations_matrices.go (`scalars.condition_number`) | "an index above about 30 where two or more variables put a large share of their variance on that dimension" | Belsley, Kuh & Welsch (1980) | The source's rule is an index of 30 or more with variance-decomposition proportions above 0.5 for two or more variables. "A large share" is unquantified. | State 0.5. | open |
| MR-07 | low | `MAT_PARTIAL_CORRELATION` | purposes_matrices.go (KnownAs) | "controlling for" | Pulse routing (U23 synonym tier) | A phrase analysts also use for regression adjustment; it can route a "controlling for age" regression question to the partial matrix. | Drop the alias or keep it, deliberately (it sits behind `REG_OLS` in NotFor). | open |
| MR-08 | low | `MAT_RELIABILITY` | interpretations_matrices.go (alpha bands) | George & Mallery (2003) alpha bands | Gliem & Gliem (2003, p. 87) | The bands were transcribed from Gliem & Gliem's secondary quotation; George & Mallery (2003) itself was not fetched. | Verify against the primary text (U33 reviewer). | open |

## Open owner calls

Left for the maintainer; none was decided by the docs story. Each changes behaviour or a frozen artefact.

- **(a) Bare `spearman` / `kendall` aliases.** They stay `KnownAs` on `TEST_SPEARMAN_R` / `TEST_KENDALL_TAU` (the uniqueness gate forbids duplicates); `MAT_CORRELATION` carries "rank correlation", "spearman correlation matrix", "kendall correlation matrix". Decide whether the bare names should move to the matrix.
- **(b) `examples/profiles/read-only-analyst.json` edited in place** for `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`, `MAT_PCA` and `MAT_COLLINEARITY` (the `ATTR_CODE_IN` precedent). Decide whether the "frozen" rule should bite before v1.0.0.
- **(c) Non-converged minres.** `MAT_RELIABILITY` keeps omega (the last iterate) beside `PULSE_MATRIX_NOT_CONVERGED`; the alternative is a null omega. Decide, then align MR-05.
- **(d) `MAT_COLLINEARITY` shape.** `variance_decomposition` is TRANSPOSED relative to `perturb` (rows are variables, columns `D1..Dq` in condition-index order) to honour the rectangular kind's "rows are members" rule; the intercept key is `(intercept)` (the oracle says `intercept`); `scalars.max_vif` was added so the VIF reading has a field to attach to. Confirm or change before the wire is frozen.
- **(e) Regression `vcov` refusal.** Resample and Selection modifiers are refused with `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (their refits compute no covariance). Selection could instead report the selected subset's covariance, which needs the refit path to carry it.
- **(f) `mv_vcov.json` at converged weights.** Regenerating the GLM cases with the IRLS refined to a fixed point would let the GLM tolerance drop from 1e-6 to about 1e-10 (needs `make reference` on the pinned toolchain). Filed with the oracle work as TODO #266.

## Open items for the U33 human reviewer (U24)

- **MR-01 to MR-08** above, and owner calls (a) to (f).
- **Omega form.** `MAT_RELIABILITY` uses the model form (Σλ)² / ((Σλ)² + Σψ); `psych::omega`'s `omega.tot` uses the observed sum of R and diverges on misfit data (up to about 0.24 on `reversed_items`). Confirm the model form as the documented choice.
- **Heywood handling.** Pulse returns the unconstrained minres optimum and flags `ψ ≤ 0` (omega null with `PULSE_MATRIX_HEYWOOD`); psych bounds ψ and stops on an optimiser-path artefact. Confirm withholding omega is the right behaviour.
- **Pairwise PCA basis.** PCA on `cor(use = "pairwise")` with the smallest pair's N* for Bartlett (`PULSE_MATRIX_PAIRWISE_N_STAR`) has no external oracle (TODO #266).
- **Probability-weighted `item_sd` under pairwise deletion** uses the slot-wide n_eff factor (no per-pair Σw² in the co-moment state); no oracle case covers it (TODO #270).
- **REG_GLM gamma `vcov`** uses dispersion 1 as the standard errors do; R's `vcov(glm, gamma)` estimates it (TODO #271).
- **Alpha bands and the Kendall ratio** against their primary sources (MR-03, MR-08).

## Panel independence

One pass of one model family that also wrote the texts. The deterministic layers above are the stronger guarantee: for `frequency` and unweighted input every figure is pinned to stock R and the named CRAN packages at the stated tolerance; for `probability` they guarantee the one-rule arithmetic and scale invariance only (no software implements Kish-w* inference).
