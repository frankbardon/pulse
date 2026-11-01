# gen_distributed_lag.R — R oracle for WIN_DISTRIBUTED_LAG (custom-regressions E3-S3;
# gen_reference.R — R oracle for Pulse's shared statistical primitives.
#
# Writes one JSON golden per distribution family into the output
# directory (default internal/processing/testdata/reference). Every
# number is emitted with sprintf("%.17g"), which round-trips a double
# exactly, so the Go oracle tests read back the very doubles R used.
#
# Run via `make reference` (which also appends the `// golden-hash:`
# footer TestGoldensNotHandEdited checks). CI never runs R: the goldens
# are committed and read-only there.
#
# Requirements: base R + jsonlite for the primitives; the multivariate
# half (gen_multivariate.R, sourced at the end) pins psych, car, ppcor,
# perturb, corpcor and Matrix — see its header for versions and install.
#
# Grid choices worth knowing:
#   * Inputs are filtered to reference values >= 1e-300 (the smallest
#     comfortably-normal double) so the far tail is exercised wherever it
#     is representable.
#   * Student-t df stops at 1e5: R's pt()/qt() switch to a normal
#     approximation above df = 4e5, which is not an oracle.
#   * Student-t quantiles are qt() POLISHED by Newton steps on
#     log pt(): in the far tail qt() disagrees with R's own pt() (by
#     1.5% in p at df = 1.5, p = 1e-300; ~2e-8 at df = 3), while pt()
#     (TOMS 708) is accurate there. The raw qt() value is kept as
#     r_qt for reference.
#   * The studentized range reference is NOT ptukey()/qtukey(). R's
#     ptukey() is accurate to only ~1e-8 relative for df >= 10 (worse at
#     small df: 2e-5 at df = 2, k = 2, where the closed form
#     P(|T| > q/sqrt(2)) is exact) and computes the upper tail as
#     1 - CDF; qtukey() is documented to ~4 decimal places. The oracle is a
#     nested stats::integrate() of the survival function written in a
#     cancellation-free form; ptukey()/qtukey() are recorded alongside as
#     an independent cross-check.

suppressPackageStartupMessages(library(jsonlite))

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}

meta <- function(primitive, r_fn, note) {
  list(
    generator = "scripts/reference/gen_reference.R",
    r_version = R.version.string,
    packages = list(
      stats = as.character(packageVersion("stats")),
      jsonlite = as.character(packageVersion("jsonlite"))
    ),
    primitive = primitive,
    r_function = r_fn,
    note = note
  )
}

write_golden <- function(name, doc) {
  path <- file.path(out_dir, paste0(name, ".json"))
  txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
  # writeLines would add a trailing newline; the golden-hash footer
  # (appended by `make reference`) starts with its own newline.
  cat(txt, file = path)
  message("wrote ", path, " (", length(doc$cases), " cases)")
}

keep <- function(v) is.finite(v) && v >= 1e-300

# ---------------------------------------------------------------- Student t
t_df <- c(1, 1.5, 2, 3, 4.7, 5, 7.25, 10, 23.37, 30, 100, 1000, 1e4, 1e5)
t_stat <- c(1e-3, 0.1, 0.5, 1, 1.5, 1.96, 2.5, 3, 4, 6, 8, 12, 20, 30, 50,
            100, 300, 1e3, 1e4, 1e6, 1e9, 1e12, 1e15)

t_cases <- list()
for (df in t_df) for (t in t_stat) {
  p2 <- 2 * pt(-t, df)
  if (!keep(p2)) next
  t_cases[[length(t_cases) + 1]] <- list(
    t = num(t), df = num(df),
    p_two_sided = num(p2),
    cdf_neg = num(pt(-t, df)),
    cdf_pos = num(pt(t, df))
  )
}
write_golden("student_t_p", c(meta(
  "studentTTwoSidedP / studentTCDF",
  "2*pt(-|t|, df); pt(-t, df); pt(t, df)",
  "cdf_neg is the lower tail at -t, cdf_pos the CDF at +t."
), list(cases = t_cases)))

tq_p <- c(1e-300, 1e-200, 1e-100, 1e-50, 1e-20, 1e-12, 1e-8, 1e-5, 1e-3, 0.01,
          0.025, 0.05, 0.1, 0.25, 0.4, 0.49, 0.51, 0.6, 0.75, 0.9, 0.95,
          0.975, 0.99, 0.999, 1 - 1e-5, 1 - 1e-8)
# Newton-polish a qt() quantile so pt(q, df, lower.tail) == p to
# working precision. Steps on the log probability, whose derivative is
# +/- dt/pt.
polish_qt <- function(p, df, lower.tail = TRUE) {
  q <- qt(p, df, lower.tail = lower.tail)
  if (!is.finite(q) || q == 0) return(q)
  for (i in 1:100) {
    lp <- pt(q, df, lower.tail = lower.tail, log.p = TRUE)
    slope <- exp(dt(q, df, log = TRUE) - lp)
    if (!lower.tail) slope <- -slope
    qn <- q - (lp - log(p)) / slope
    if (!is.finite(qn) || sign(qn) != sign(q)) qn <- q / 2 + qn / 2
    done <- abs(qn - q) <= 4 * .Machine$double.eps * abs(qn)
    q <- qn
    if (done) break
  }
  resid <- pt(q, df, lower.tail = lower.tail, log.p = TRUE) - log(p)
  # The residual is in log p, which is ~ -690 at p = 1e-300; allow a few
  # ulps of that magnitude (relative error in p stays below 1e-11).
  if (abs(resid) > 1e-14 * max(1, abs(log(p)))) stop(sprintf("qt polish failed: p=%g df=%g resid=%g", p, df, resid))
  q
}

tq_cases <- list()
for (df in t_df) for (p in tq_p) {
  q <- polish_qt(p, df)
  if (!is.finite(q)) next
  # The lower tail quantile magnitude grows like p^(-1/df); keep it
  # representable and well inside the double range.
  if (abs(q) > 1e290) next
  tq_cases[[length(tq_cases) + 1]] <- list(
    p = num(p), df = num(df), q = num(q), r_qt = num(qt(p, df))
  )
}
write_golden("student_t_quantile", c(meta(
  "studentTQuantile",
  "qt(p, df) polished by Newton on log pt()",
  "Lower-tail quantile: P(T <= q) = p; r_qt is the unpolished qt()."
), list(cases = tq_cases)))

alpha_grid <- c(0.9, 0.5, 0.2, 0.1, 0.05, 0.01, 0.001, 1e-5, 1e-8, 1e-12, 1e-20)
ti_cases <- list()
for (df in t_df) for (a in alpha_grid) {
  q <- polish_qt(a / 2, df, lower.tail = FALSE)
  if (!is.finite(q) || q > 1e290) next
  ti_cases[[length(ti_cases) + 1]] <- list(
    alpha = num(a), df = num(df), q = num(q),
    r_qt = num(qt(a / 2, df, lower.tail = FALSE))
  )
}
write_golden("student_t_inverse_two_sided", c(meta(
  "studentTInverseTwoSided",
  "qt(alpha/2, df, lower.tail = FALSE) polished by Newton on log pt()",
  "Two-sided critical value: P(|T| >= q) = alpha; r_qt is the unpolished qt()."
), list(cases = ti_cases)))

# ------------------------------------------------------------- chi-square
chi_df <- c(0.5, 1, 2, 3, 4.5, 5, 10, 17.3, 30, 100, 1000, 1e4, 1e5)
chi_mult <- c(1e-3, 0.01, 0.1, 0.5, 0.9, 1, 1.1, 1.5, 2, 3, 5, 10, 30)
chi_abs <- c(1e-6, 0.01, 1, 10, 50, 100, 300, 600, 1000, 1350)
chi_cases <- list()
for (df in chi_df) for (x in sort(unique(c(df * chi_mult, chi_abs)))) {
  p <- pchisq(x, df, lower.tail = FALSE)
  if (!keep(p)) next
  chi_cases[[length(chi_cases) + 1]] <- list(x = num(x), df = num(df), p = num(p))
}
write_golden("chi_square_survival", c(meta(
  "chiSquareSurvival",
  "pchisq(x, df, lower.tail = FALSE)",
  "Upper-tail probability P(X >= x)."
), list(cases = chi_cases)))

# ------------------------------------------------- noncentral chi-square
# The lower-tail CDF behind MAT_FACTOR's RMSEA interval (TODO #405). R's
# pchisq(x, df, ncp) sums the Poisson mixture to full double precision
# for ncp < 80 and switches to an approximation bounded at 1e-12
# absolute above it, so the grid keeps ncp < 80 (the oracle) and the
# ncp >= 80 rows only where the CDF is in [1e-3, 1 - 1e-3], marked
# r_approx, which the Go test holds to an absolute 1e-11.
nc_df <- c(0.5, 1, 2, 3, 5, 10, 17.3, 30, 100)
nc_ncp <- c(0.01, 0.5, 1, 2.5, 5, 10, 22.7, 40, 79, 80, 120, 300)
nc_mult <- c(0.01, 0.1, 0.3, 0.5, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 2, 3, 5)
nc_cases <- list()
for (df in nc_df) for (ncp in nc_ncp) for (m in nc_mult) {
  x <- (df + ncp) * m
  p <- pchisq(x, df, ncp = ncp)
  approx <- ncp >= 80
  if (!keep(p) || p >= 1) next
  if (approx && (p < 1e-3 || p > 1 - 1e-3)) next
  nc_cases[[length(nc_cases) + 1]] <- list(x = num(x), df = num(df), ncp = num(ncp), cdf = num(p), r_approx = approx)
}
write_golden("noncentral_chi_square_cdf", c(meta(
  "noncentralChiSquareCDF",
  "pchisq(x, df, ncp = ncp)",
  "Lower-tail probability P(X <= x) of a noncentral chi-square; r_approx marks ncp >= 80, where R's own algorithm is accurate to 1e-12 absolute only."
), list(cases = nc_cases)))

# ---------------------------------------------------------------------- F
f_df1 <- c(1, 2, 3, 5, 10, 30, 100, 1000)
f_df2 <- c(1, 2, 5, 10, 17.3, 30, 100, 1000, 1e5)
f_stat <- c(1e-3, 0.1, 0.5, 1, 2, 4, 10, 50, 1e3, 1e6, 1e10)
f_cases <- list()
for (d1 in f_df1) for (d2 in f_df2) for (f in f_stat) {
  p <- pf(f, d1, d2, lower.tail = FALSE)
  if (!keep(p)) next
  f_cases[[length(f_cases) + 1]] <- list(f = num(f), df1 = num(d1), df2 = num(d2), p = num(p))
}
write_golden("f_survival", c(meta(
  "fSurvival",
  "pf(f, df1, df2, lower.tail = FALSE)",
  "Upper-tail probability P(F >= f)."
), list(cases = f_cases)))

# ----------------------------------------------------------------- normal
z_grid <- c(-37.5, -37, -30, -20, -12, -10, -8, -6, -5, -4, -3, -2.5, -1.96,
            -1.5, -1, -0.5, -0.1, -1e-3, 0, 1e-3, 0.1, 0.5, 1, 1.5, 1.96, 2.5,
            3, 4, 5, 6, 8)
n_cases <- list()
for (z in z_grid) {
  p <- pnorm(z)
  if (!keep(p)) next
  n_cases[[length(n_cases) + 1]] <- list(z = num(z), cdf = num(p))
}
write_golden("normal_cdf", c(meta(
  "standardNormalCDF",
  "pnorm(z)",
  "Lower-tail CDF Phi(z)."
), list(cases = n_cases)))

np_grid <- c(1e-300, 1e-200, 1e-100, 1e-50, 1e-20, 1e-10, 1e-5, 1e-3, 0.01,
             0.02, 0.02425, 0.025, 0.05, 0.1, 0.3, 0.45, 0.5, 0.55, 0.7, 0.9,
             0.95, 0.975, 0.97575, 0.98, 0.99, 0.999, 1 - 1e-5, 1 - 1e-10,
             1 - 1e-15)
np_cases <- list()
for (p in np_grid) {
  np_cases[[length(np_cases) + 1]] <- list(p = num(p), q = num(qnorm(p)))
}
write_golden("normal_ppf", c(meta(
  "standardNormalPPF",
  "qnorm(p)",
  "Inverse CDF; q = 0 at p = 0.5 is compared with an absolute floor."
), list(cases = np_cases)))

# ------------------------------------------------------------- Kolmogorov
ks_lambda <- c(0.15, 0.2, 0.25, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1, 1.1,
               1.2, 1.36, 1.5, 1.63, 2, 2.5, 3, 4, 5, 7, 10, 12, 15, 18.5)
ks_cases <- list()
for (l in ks_lambda) {
  # Limiting distribution of sqrt(n) * D_n (two-sided); an essentially
  # zero tolerance runs the series until its terms underflow.
  p <- .Call(stats:::C_pkolmogorov_two_limit, l, FALSE, 1e-300)
  if (!keep(p)) next
  ks_cases[[length(ks_cases) + 1]] <- list(lambda = num(l), p = num(p))
}
write_golden("kolmogorov_survival", c(meta(
  "kolmogorovTwoLimitUpper",
  "stats:::C_pkolmogorov_two_limit(lambda, lower.tail = FALSE, tol = 1e-300)",
  "Q_KS(lambda) = 2 * sum_{j>=1} (-1)^(j-1) exp(-2 j^2 lambda^2), the limiting Kolmogorov survival."
), list(cases = ks_cases)))

# ------------------------------------------------------ studentized range
# Survival of the range of k iid N(0,1), written without cancellation:
#   P(R > t) = k * int phi(z) * ( a^(k-1) - (a - c)^(k-1) ) dz
#   a = P(Z > z), c = P(Z > z + t)
#          = -k * int phi(z) a^(k-1) expm1((k-1) log1p(-c/a)) dz
range_surv <- function(t, k) {
  sapply(t, function(tt) {
    if (tt <= 0) return(1)
    f <- function(z) {
      la <- pnorm(z, lower.tail = FALSE, log.p = TRUE)
      lc <- pnorm(z + tt, lower.tail = FALSE, log.p = TRUE)
      v <- -k * exp(dnorm(z, log = TRUE) + (k - 1) * la) *
        expm1((k - 1) * log1p(-exp(lc - la)))
      v[!is.finite(v)] <- 0
      v
    }
    mid <- -tt / 2
    integ(f, -Inf, mid) + integ(f, mid, Inf)
  })
}

# integrate() with a 1e-13 relative target. A segment whose integrand
# has underflowed to ~0 (the far tail of the outer integral) trips
# integrate()'s divergence heuristic under abs.tol = 0; the retry gives
# it an absolute floor 1e-16 below the mass accumulated so far, which
# cannot move the result.
integ <- function(f, lo, hi, floor_abs = 0) {
  r <- tryCatch(
    integrate(f, lo, hi, rel.tol = 1e-13, abs.tol = 0, subdivisions = 5000L)$value,
    error = function(e) NULL
  )
  if (is.null(r)) {
    r <- integrate(f, lo, hi, rel.tol = 1e-13, abs.tol = max(floor_abs, 1e-320),
                   subdivisions = 5000L)$value
  }
  r
}

tukey_surv <- function(q, k, df) {
  lconst <- log(2) + (df / 2) * log(df / 2) - lgamma(df / 2)
  g <- function(s) {
    out <- numeric(length(s))
    pos <- s > 0
    lh <- lconst + (df - 1) * log(s[pos]) - df * s[pos]^2 / 2
    out[pos] <- exp(lh) * range_surv(q * s[pos], k)
    out
  }
  sig <- 1 / sqrt(2 * df)
  brk <- sort(unique(c(0, pmax(0, 1 + sig * c(-40, -20, -10, -5, -2, 0, 2, 5, 10, 20, 40)))))
  total <- 0
  for (i in seq_len(length(brk) - 1)) {
    total <- total + integ(g, brk[i], brk[i + 1], 1e-16 * total)
  }
  total + integ(g, brk[length(brk)], Inf, 1e-16 * total)
}

tk_k <- c(2, 3, 5, 10, 20)
tk_df <- c(2, 5, 10, 30, 120, 1000, 1e4)
tk_q <- c(0.5, 1, 2, 3, 4, 6, 9, 15)
tk_cases <- list()
for (k in tk_k) for (df in tk_df) for (q in tk_q) {
  p <- tukey_surv(q, k, df)
  if (!keep(p)) next
  tk_cases[[length(tk_cases) + 1]] <- list(
    q = num(q), k = num(k), df = num(df), p = num(p),
    r_ptukey_upper = num(ptukey(q, k, df, lower.tail = FALSE))
  )
}
write_golden("studentized_range_survival", c(meta(
  "studentizedRangeSurvival",
  "nested integrate() of the cancellation-free survival; ptukey(q, k, df, lower.tail = FALSE) as cross-check",
  "p is the oracle (relative accuracy ~1e-12); r_ptukey_upper is R's ptukey, accurate to ~1e-8."
), list(cases = tk_cases)))

tki_alpha <- c(0.2, 0.1, 0.05, 0.01, 0.001)
tki_df <- c(2, 5, 10, 30, 120, 1000)
tki_cases <- list()
for (k in tk_k) for (df in tki_df) for (a in tki_alpha) {
  r_q <- qtukey(a, k, df, lower.tail = FALSE)
  # Bracket around R's qtukey (accurate to ~1e-4, worse at df = 2) and
  # refine on the log of the oracle survival.
  fn <- function(q) log(tukey_surv(q, k, df)) - log(a)
  root <- uniroot(fn, c(r_q * 0.9, r_q * 1.1), extendInt = "downX",
                  tol = 1e-14 * r_q, maxiter = 200)$root
  tki_cases[[length(tki_cases) + 1]] <- list(
    alpha = num(a), k = num(k), df = num(df), q = num(root),
    r_qtukey = num(r_q)
  )
}
write_golden("studentized_range_inverse", c(meta(
  "studentizedRangeInverse",
  "uniroot() on the oracle survival; qtukey(alpha, k, df, lower.tail = FALSE) as cross-check",
  "q solves P(Q > q) = alpha; r_qtukey is R's qtukey, accurate to ~1e-4."
), list(cases = tki_cases)))

# ------------------------------------------------- multivariate (U24)
# The matrix-operator goldens (mv_*.json) and their fixtures live in a
# sibling script that reuses out_dir, num() and write_golden() above;
# its header pins psych, car, ppcor, perturb, corpcor and Matrix.
script_dir <- local({
  f <- sub("^--file=", "", grep("^--file=", commandArgs(trailingOnly = FALSE), value = TRUE))
  if (length(f) == 1) dirname(normalizePath(f)) else "scripts/reference"
})
source(file.path(script_dir, "gen_multivariate.R"), local = FALSE)

# The OVERLAY_STD_RESIDUAL golden (std_residual.json: chisq.test
# adjusted residuals, expected counts, two-sided normal p and per-row
# p.adjust); base stats only, reuses out_dir and write_golden().
source(file.path(script_dir, "gen_std_residual.R"), local = FALSE)

# The OVERLAY_CORRESPONDENCE golden (correspondence.json: ca::ca
# inertias, masses, standard / map coordinates under the three
# normalizations, quality and contribution); needs the ca package.
source(file.path(script_dir, "gen_correspondence.R"), local = FALSE)

# The OVERLAY_MARKOV golden (markov.json: transition matrices, n-step
# powers, projected distributions, steady states, period /
# irreducibility, mean absorption times); needs the markovchain package.
source(file.path(script_dir, "gen_markov.R"), local = FALSE)

# The OVERLAY_RAKE golden (rake.json: 2-D iterative proportional fits to
# share and count targets, structural zeros, a capped non-converged fit,
# adjustment factors); base stats loglin, cross-checked by survey::rake.
source(file.path(script_dir, "gen_rake.R"), local = FALSE)

# The OVERLAY_SYMMETRY golden (symmetry.json: mcnemar.test McNemar /
# Bowker statistics, df and p, with and without the 2 x 2 continuity
# correction, plus the empty-pair drop Pulse applies); base stats only.
source(file.path(script_dir, "gen_symmetry.R"), local = FALSE)

# The matrix params.inference golden (matrix_inference.json: per-pair
# cor.test p-values and Fisher-z intervals on MAT_CORRELATION, ppcor
# partial-correlation p and intervals on df = N* - 2 - g); reads the
# fixture CSVs gen_multivariate.R wrote; base stats + ppcor.
source(file.path(script_dir, "gen_matrix_inference.R"), local = FALSE)

# The matrix multiplicity golden (matrix_multiplicity.json: p.adjust
# bonferroni / holm / BH / BY over the upper-triangle cor.test / ppcor
# p-values of a correlation matrix, per result or pooled across buckets
# and specs); base stats + ppcor.
source(file.path(script_dir, "gen_matrix_multiplicity.R"), local = FALSE)

# The REG_GLM IRLS golden (glm_irls.json: two-level designs whose MLE has
# a closed form — the credit-risk showcase's default x collections_flag
# table among them — fitted by glm and checked against the closed form,
# plus complete and quasi-complete separations glm itself flags, and an
# unseparated fit with fitted probabilities down to ~7e-13); base stats
# only.
source(file.path(script_dir, "gen_glm_irls.R"), local = FALSE)

# The penalised REG_GLM golden (glm_penalty.json: l2 / l1 / elasticnet
# on binomial and poisson, weighted and separated cases, under glmnet's
# objective with standardize = FALSE, KKT-self-checked); glmnet + jsonlite.
source(file.path(script_dir, "gen_glm_penalty.R"), local = FALSE)

# The penalised REG_OLS golden (ols_penalty.json: l2 / l1 / elasticnet
# on the shared standardize = FALSE objective, glmnet on the unit-SD
# response, KKT-self-checked); glmnet + jsonlite.
source(file.path(script_dir, "gen_ols_penalty.R"), local = FALSE)

# The unpenalised REG_OLS golden (ols_lm.json: lm() coefficients, SE, t,
# p, confint, R², adjusted R², sigma and df, plain and frequency-weighted
# on the rep() expansion, normal-equation self-checked; a large-offset
# predictor fitted on its exact shift); base stats + jsonlite.
source(file.path(script_dir, "gen_ols_lm.R"), local = FALSE)

# The AGG_AUC golden (auc.json: pROC rank-sum AUC with ties half and its
# DeLong se / 95% interval, plus weighted cases computed as the weighted
# Mann-Whitney double sum); pROC + jsonlite.
source(file.path(script_dir, "gen_auc.R"), local = FALSE)

# The REG_OLS / REG_GLM jackknife golden (jackknife.json: delete-a-group
# by cluster and by a group assignment, frequency-weighted (JK1),
# l2-penalised and weighted leave-one-out cases, all by manual deletions;
# the cluster cases cross-checked against survey's JK1 design); survey +
# glmnet + jsonlite.
source(file.path(script_dir, "gen_jackknife.R"), local = FALSE)

# The model-workbench end-to-end golden (credit_workbench.json + the
# committed fixture credit/loans.csv): fit_on target encoding, ridge
# REG_GLM with a delete-a-vintage jackknife, holdout AUC (pROC DeLong)
# and the holdout decile gains table; glmnet + pROC + jsonlite.
source(file.path(script_dir, "gen_credit_workbench.R"), local = FALSE)

# The assembled TEST_* output goldens (test_anova_rm / test_tukey_hsd /
# test_trend / test_ks / test_kendall_tau / test_pearson_r /
# test_shapiro_wilk / test_brown_forsythe.json): stock stats on inline
# seeded inputs, the Tukey figures refined on tukey_surv() above,
# Kendall::MannKendall as a single-precision cross-check,
# nortest::sf.test for Shapiro-Francia; base stats + Kendall + nortest +
# jsonlite.
source(file.path(script_dir, "gen_test_outputs.R"), local = FALSE)

# The post-test twins over rows holding nulls (test_post_nulls.json): one
# case per post family at R's default NA handling (listwise), reusing the
# helpers of gen_test_outputs.R above; base stats + nortest + jsonlite.
source(file.path(script_dir, "gen_post_tests.R"), local = FALSE)

# The REG_GLM gamma golden (glm_gamma.json: summary.glm's estimated
# dispersion, t-based p-values and intervals, vcov(glm) on three seeded
# or inline designs, dispersion self-checked against the Pearson
# chi-square); base stats + jsonlite. Draws its own seeded rows last.
source(file.path(script_dir, "gen_glm_gamma.R"), local = FALSE)

# The REG_BAYES_LINEAR golden (bayes_linear.json: the Normal-Inverse-Gamma
# conjugate posterior by R matrix algebra, self-checked against lm.fit on
# the augmented design; default and informative priors, plain and
# frequency-weighted on the rep() expansion); base stats + jsonlite.
# Deterministic rows, no RNG.
source(file.path(script_dir, "gen_bayes_linear.R"), local = FALSE)

# The REG_OLS bounds.nonnegative golden (ols_nnls.json: non-negative
# least squares on named slopes, the intercept and other slopes free —
# nnls::nnls on the centred, sqrt(w)-scaled design, self-checked against
# the KKT conditions and lm.fit on the passive set; no-binding, binding,
# mixed free, two-binding, near-collinear and frequency-weighted cases);
# nnls 1.6 + jsonlite. Draws its own seeded rows.
source(file.path(script_dir, "gen_ols_nnls.R"), local = FALSE)

# The REG_BAYES_LINEAR per-coefficient prior golden (bayes_precision.json:
# prior_precision as a vector, Lambda0 = diag(v) with flat (0) and strong
# entries, plain and frequency-weighted; the same conjugate closed form,
# self-checked against lm.fit on the augmented design); base stats +
# jsonlite. Deterministic rows, no RNG.
source(file.path(script_dir, "gen_bayes_precision.R"), local = FALSE)

# The REG_GLM gamma selection golden (glm_gamma_selection.json: AIC(fit) /
# BIC(fit) per candidate model on the gamma log-likelihood at dispersion
# deviance / n, plain, frequency (the rep() expansion) and probability
# (w*, BIC on n_eff); step()'s forward / backward / both paths, the
# fixed-dispersion score's diverging ones); base stats + jsonlite. Draws
# its own seeded rows last.
source(file.path(script_dir, "gen_glm_gamma_selection.R"), local = FALSE)

# The inferential overlay goldens (overlay_chisq_vs_pop / _chisq_vs_ref /
# _ks_vs_pop / _pairwise_probit_t / _fisher_exact_cell / _two_sample /
# _weighted_unit / _mean_vs_ref.json): every expected overlay figure
# computed by R from the overlay's inputs, never by the helper the
# overlay calls (U36 E3-S1); base stats + jsonlite. Draws its own seeded
# rows last.
source(file.path(script_dir, "gen_overlays.R"), local = FALSE)

# The WIN_DISTRIBUTED_LAG golden (distributed_lag.json: the geometric / Weibull CDF /
# Weibull PDF lag kernels, normalised and raw, and their convolution over
# a two-partition series with nulls, via stats::filter); base stats +
# jsonlite. Deterministic rows, no RNG.
source(file.path(script_dir, "gen_distributed_lag.R"), local = FALSE)

# The bound-fit influence golden (glm_influence.json: hatvalues,
# rstandard, rstudent, cooks.distance and dffits over lm and the
# canonical-link GLMs, each re-derived from its R 4.6.1 closed form);
# base stats + jsonlite. Seeded RNG.
source(file.path(script_dir, "gen_glm_influence.R"), local = FALSE)

# The non-canonical REG_GLM links golden (glm_links.json: probit,
# cloglog, poisson identity / sqrt, gamma log / identity — coefficients,
# SEs, vcov, deviances, AIC, dispersion, per-row fitted / working / hat /
# dffits, sandwich vcovHC HC0 / HC1 / HC3, and frequency-weighted cases
# against the rep() expansion; each figure re-derived from its closed
# form); base stats + jsonlite + sandwich 3.1-3. Seeded RNG.
source(file.path(script_dir, "gen_glm_links.R"), local = FALSE)

# The REG_GLM negative binomial golden (glm_nb.json: NB2 with theta
# estimated on the log / sqrt / identity links — glm.nb's joint MLE
# pushed to its fixed point — or fixed through negative.binomial(theta):
# coefficients, SEs, vcov at dispersion 1, theta, SE(theta), 2 loglik,
# AIC, deviances, sandwich vcovHC HC0 / HC1 / HC3, per-row residuals and
# hat values, an influence case, and a frequency-weighted case against
# the rep() expansion; each figure re-derived from its closed form);
# base stats + jsonlite + MASS 7.3-66 + sandwich 3.1-3. Seeded RNG.
source(file.path(script_dir, "gen_glm_nb.R"), local = FALSE)

# The regression marginal-effects golden (margins.json: average marginal
# effects and marginal effects at the mean on lm and the binomial logit /
# probit / cloglog, poisson log, gamma inverse / log and negative binomial
# glm — response and link scale, classical / HC3 / cluster vcov, and a
# frequency-weighted pair against the rep() expansion; the analytic
# closed forms with their delta-method SEs, cross-checked against
# marginaleffects 1.0.0); base stats + jsonlite + MASS 7.3-66 + sandwich
# 3.1-3 (+ marginaleffects, skipped with a message when absent). Seeded RNG.
source(file.path(script_dir, "gen_margins.R"), local = FALSE)

# The regression log-likelihood golden (loglik.json: logLik / AIC of lm
# with and without an intercept and of the binomial-logit, poisson-log
# and gamma-inverse glm, each with a frequency-weighted case against the
# rep() expansion; each figure re-derived from its closed form); base
# stats + jsonlite. Seeded RNG.
source(file.path(script_dir, "gen_loglik.R"), local = FALSE)

# The regression variance-inflation golden (vif.json: car::vif's GVIF,
# Df and GVIF^(1/(2*Df)) on lm and the binomial logit, poisson log,
# fixed-theta negative binomial and gamma log glm — ungrouped and with a
# factor's dummies as one term, on the fit (basis model) and on lm over
# the prior weights (basis predictors), frequency weights against the
# rep() expansion and probability weights; each figure re-derived from
# the weighted design's closed form); base stats + jsonlite + MASS
# 7.3-66 + car 3.1-5. Seeded RNG.
source(file.path(script_dir, "gen_vif.R"), local = FALSE)
