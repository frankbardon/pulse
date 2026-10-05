# Weighted reference fixtures

`gen_weight_reference.py` writes `../../weight_reference_values_test.go`,
the external reference values `TestWeightReferenceValues` and
`TestWeightReferenceKindsAgree` pin (contract:
`.claude/reference/weighting.md`, "Reference fixtures"). CI never runs
Python or R; the generated Go file is the oracle. Never hand-edit it.

## Regenerate

```sh
cd internal/service/testdata/weight_reference
# once: R 4.6.1 + Hmisc 5.3.0 in a private library (see hmisc_quantile.R)
mkdir -p rlib
R_LIBS=rlib Rscript -e 'install.packages("remotes", repos = "https://cloud.r-project.org")' \
  -e 'remotes::install_version("Hmisc", "5.3.0", repos = "https://cloud.r-project.org")'
R_LIBS=rlib uv run --no-project gen_weight_reference.py   # Python versions pinned in the PEP 723 header
gofmt -w ../../weight_reference_values_test.go
go test ../.. -run TestWeightReference
```

(Keep `rlib/` out of the commit.) Pinned toolchain: Python 3.12, numpy
2.3.3, scipy 1.16.2, statsmodels 0.14.5, scikit-learn 1.7.2; R 4.6.1,
Hmisc 5.3.0 (the R scripts refuse any other R / Hmisc version). The script's docstring maps
each Pulse definition onto the library call configured to match it.

## Weighted significance tests

The tier-1 test references (`weightRefTestRows` / `weightRefTestCases`,
pinned by `TestWeightReferenceValues/tests`) come from the same run:

- kind `frequency` — stock R on the `rep()`-expanded rows, via
  `test_reference.R` (base R `stats` only, no package to install; the
  script refuses any R but 4.6.1). The frequency-only rank tests
  (`FREQUENCY_ONLY`: `wilcox.test`, `kruskal.test`,
  `cor.test(exact = FALSE)` for Spearman / Kendall with continuity) have
  only this row, cross-checked against scipy on the same expansion
  (Kendall: tau only — scipy's p omits the continuity correction).
  Fisher / KS / Brown-Forsythe (E2-S2) likewise: `fisher.test` p (its
  estimate is the conditional MLE, so the sample odds ratio is
  scipy-only), `ks.test` D (Pulse's p is the Stephens-corrected
  asymptotic, pinned via `scipy.special.kolmogorov`), and
  `anova(lm(|x − group median| ~ k))` — what `car::leveneTest(center =
  median)` computes, in base R, so no package pin;
- kind `probability` — the w* closed form in the generator, its moment
  step cross-checked against statsmodels (`DescrStatsW` on w*,
  `CompareMeans`, `proportions_ztest`) and scipy `chi2_contingency`. The
  same closed form must reproduce R on the frequency column (1e-9) or
  the generator aborts.

A case is a JSON request fragment plus figures keyed by wire path
(`tests[0].details.mean[a]`; a `[label]` index resolves through the
sibling `groups` array), so a later inferential surface (rank tests,
regressions, attributes, CIs) adds rows to the generator, not a new
harness.

## Weighted inferential overlays

The overlay references (`weightRefOverlayCases`, pinned by
`TestWeightReferenceValues/overlays`) reuse the significance-test
fixture and the same run. Each case is one host — a Process crosstab
(`Request.Overlays`) or a Compose request whose slots are every row,
`y > 4` and `y < 6` — and its figures are keyed `<layer>/<path>`
(`cell[<row>|<col>]`, `#i` an element of a panel cell, `entry[<key>]`):

- kind `frequency` — stock R on the expanded rows (`test_reference.R`,
  the `ov_*` cases, which print the Go keys directly): `chisq.test` on
  the h × o table, and as a goodness-of-fit per row / column / against
  the reference distribution (`OVERLAY_CHISQ_*`); `fisher.test` on each
  cell's 2 × 2 (cell vs the rest of its row and column); `t.test`
  (Welch) for the pairwise and Compose t kinds; `prop.test(correct =
  FALSE)` for the proportion kinds; the normal tail on R's mean / var
  for the z kinds;
- kind `probability` — the closed form (`ov_closed_form`): each table,
  row, column or row base scaled by c = Kish n_eff / Σw of the rows it
  covers, each mean leg read on w*. It must equal R on the frequency
  configuration (1e-9) or the generator aborts.

`OVERLAY_T_VS_REF` / `OVERLAY_Z_VS_REF` run on a scalar-mean series (a
weighted `AGG_AVERAGE`; spread and n from `OV_SERIES_PARAMS`, which
`test_reference.R` repeats), so the weight reaches them only through the
means. `OVERLAY_FISHER_EXACT_CELL` is frequency-only. The expected
overlay warning codes are derived from the same expected counts.

## Weighted regressions and regression attributes

The regression references (`weightRefRegRows` / `weightRefRegCases`,
pinned by `TestWeightReferenceValues/regressions`) come from the same
run, on their own fixture (x1, nullable x2; a linear, 0 / 1, count and
positive target; the `w` / `f` / `p` weight columns):

- kind `frequency` — stock R on the `rep()`-expanded rows, via
  `reg_reference.R` (base R `stats` only; refuses any R but 4.6.1): `lm`
  for `REG_OLS` and the `ATTR_REG_*` rows (`predict()`; leverage = f ×
  one copy's `hatvalues()`), `glm(control = glm.control(epsilon = 1e-14,
  maxit = 100))` with `summary(dispersion = 1)` for `REG_GLM`, refit once
  from its own converged coefficients so the standard errors are
  evaluated at the MLE (glm's otherwise use the last iteration's
  starting weights, ~1e-8 away);
- kind `probability` — the closed form on w* = w·n_eff/Σw: OLS with df
  n_eff − q (never `lm(weights =)`, whose df is the row count), its SEs
  cross-checked with statsmodels `GLM(freq_weights = w*)` (df_resid =
  Σw* − q); GLM by IRLS on w*, cross-checked the same way with scale 1;
  the attributes' scale-free closed form, leverage cross-checked with
  statsmodels OLS influence on the √w-whitened design. Each closed form
  must equal R on the frequency configuration (1e-9) first;
- penalised `REG_OLS` (l2 / l1 / elasticnet) — β only, from
  scikit-learn: `Ridge(alpha = λ·Σw)` (also checked against its closed
  form), `Lasso` / `ElasticNet(alpha = λ)` on predictors divided by their
  weighted population SD (β = coef / σ) — on the `np.repeat` expansion
  under `frequency`, `sample_weight = w` under `probability`;
- `REG_BAYES_LINEAR` (frequency-only) — no stock package fits Pulse's
  scalar-precision Normal-Inverse-Gamma prior, so its row is that
  conjugate posterior in closed form on the expanded rows (scipy t
  quantiles for the credible bounds). Frequency-expansion parity
  (`TestWeightFrequencyExpansionParity/regressions`) is its other gate.

An attribute case reads each row's value back as an opted-out `AGG_SUM`
grouped by the fixture's `id`; its figures are keyed `data[<id>].v`.

## Probability-weight quantiles: Hmisc

Pulse's `kind: probability` median / percentile IS Hmisc
`wtd.quantile(x, w, probs, type = "quantile", normwt = TRUE)`: weights
rescaled to sum to n, x₍ₖ₎ = the smallest value whose cumulative weight
is >= the 1-based rank k + 1, linear interpolation. The reference is
Hmisc itself, via `hmisc_quantile.R`; the generator asserts each Hmisc
figure equals an exact-rational transcription of the rule to 1e-12
relative (Hmisc interpolates as `(1 - f)·lo + f·hi`, Pulse as
`lo + f·(hi - lo)`, so they can differ in the last ulps).

Pulse's earlier rule (the smallest value whose normalized cumulative
weight EXCEEDS the 0-based rank) agreed with Hmisc only on integer
cumulative weights; the effort owner adopted Hmisc's. The figures that
moved:

| weights | figure | earlier Pulse | Hmisc 5.3.0 (now Pulse) |
|---|---|---|---|
| `w` probability | median | 3.5 | 4.25 |
| `w` probability | p37.5 | 3.5 | 3.5 |
| `w` probability | p90 | 6.55 | 7.275 |
| `p` probability | median | 3.5 | 3.5 |
| `p` probability | p37.5 | 2.65625 | 3.5 |
| `p` probability | p90 | 5.0 | 6.425 |
| `f` frequency (no normalization) | p90 | 6.9 | 6.9 |

## Knife-edge guard

The generator refuses a fixture where a normalized cumulative weight
lands on an integer rank. Pulse snaps any cumulative weight within 1e-9
relative of an integer to it before comparing with a rank, so its answer
there is the exact-arithmetic one on every platform; Hmisc does not
snap, so its figure on such a fixture turns on the last ulp of a float
sum and is not a usable reference.
