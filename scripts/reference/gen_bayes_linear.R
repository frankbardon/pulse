# gen_bayes_linear.R — R oracle for REG_BAYES_LINEAR (reference-oracles
# E2-S4, #203): the conjugate Normal-Inverse-Gamma posterior, computed
# by independent R matrix algebra on the design.
#
# Writes <out_dir>/bayes_linear.json. Sourced from gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_bayes_linear.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer). CI never runs R. Base stats
# + jsonlite only. No stock R package fits this prior (a scalar prior
# precision on every coefficient, intercept included), so the reference
# is the textbook conjugate update written out here:
#
#   sigma^2       ~ InvGamma(a0, b0)
#   beta | sigma^2 ~ N(mu0, sigma^2 Lambda0^-1),   Lambda0 = eps I
#
#   Lambda_n = Lambda0 + X'X                 (X with the intercept column)
#   mu_n     = Lambda_n^-1 (Lambda0 mu0 + X'y)
#   a_n      = a0 + n / 2
#   b_n      = b0 + (|y - X mu_n|^2 + (mu_n - mu0)' Lambda0 (mu_n - mu0)) / 2
#
# The b_n written here is the residual form (no cancellation); Pulse
# evaluates the equivalent b0 + (y'y + mu0'Lambda0 mu0 - mu_n'Lambda_n mu_n)/2
# from its streamed moments. Both forms are self-checked equal at 1e-12.
#
# A second, independent route self-checks every case at relative 1e-10:
# the posterior mean is the least-squares fit on the AUGMENTED data
# [X; sqrt(eps) I], [y; sqrt(eps) mu0] (lm.fit, Householder QR — never
# the normal equations), its residual sum of squares is exactly
# 2 (b_n - b0), and Lambda_n^-1 is chol2inv of that QR's R factor.
#
# Emitted figures (what RegressionResult carries for a non-robust fit):
#   coefficients     mu_n (intercept first)
#   std_errors       sqrt(b_n / a_n * diag(Lambda_n^-1)) — the scale of
#                    the marginal t_{2 a_n} posterior of each beta_j
#   credible_*       mu_n -/+ qt((1 + credible_level) / 2, 2 a_n) * SE
#   ci_*             the same interval at conf_level (credible_level
#                    when the case sets no conf_level)
#   df               2 a_n
#   residual_std_err sqrt(b_n / a_n)
#   vcov             E[sigma^2] Lambda_n^-1 = b_n / (a_n - 1) Lambda_n^-1
#                    (every case here has a_n > 1)
#   r2 / adj_r2      1 - RSS(mu_n) / TSS, adjusted on n - (p + 1)
#
# frequency_* cases are the weighted rows: integer frequency weights
# mean "this row n times", so their reference is the posterior on the
# rep()-EXPANDED rows (n = sum(w)); the direct weighted form (X'WX, X'Wy)
# is asserted equal at 1e-12. Unweighted cases carry w = 1.
#
# Rows are generated deterministically (no RNG) and stored at %.17g; the
# Go test fits the stored rows.

suppressPackageStartupMessages(library(jsonlite))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
    cat(txt, file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}

bl_num <- function(x) {
  if (length(x) != 1) stop("bl_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
bl_vec <- function(v) lapply(unname(as.numeric(v)), bl_num)
bl_mat <- function(m) lapply(seq_len(nrow(m)), function(i) bl_vec(m[i, ]))
bl_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (!all(abs(a - b) <= tol * pmax(abs(a), abs(b)))) {
    stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / pmax(abs(a), abs(b))))
  }
}

bl_cases <- list()

# bl_case computes the NIG posterior on the rep()-expansion of the rows
# by w. prior: list(mu0, eps, a0, b0); NULL entries take Pulse's
# defaults (mu0 = 0, eps = a0 = b0 = 1e-3).
bl_case <- function(name, X, y, w = rep(1, length(y)), prior = list(),
                    credible = NULL, conf = NULL) {
  stopifnot(all(w == round(w)), all(w >= 1))
  q <- ncol(X) + 1
  mu0 <- if (is.null(prior$mu0)) rep(0, q) else prior$mu0
  eps <- if (is.null(prior$eps)) 1e-3 else prior$eps
  a0 <- if (is.null(prior$a0)) 1e-3 else prior$a0
  b0 <- if (is.null(prior$b0)) 1e-3 else prior$b0
  cred <- if (is.null(credible)) 0.95 else credible
  level <- if (!is.null(conf)) conf else cred
  stopifnot(length(mu0) == q)

  idx <- rep(seq_along(y), w)
  Xd <- cbind(1, X[idx, , drop = FALSE])
  ye <- y[idx]
  n <- length(ye)

  # Route 1: the conjugate update on the design.
  L0 <- eps * diag(q)
  Ln <- L0 + crossprod(Xd)
  Lninv <- solve(Ln)
  mun <- as.numeric(Lninv %*% (L0 %*% mu0 + crossprod(Xd, ye)))
  an <- a0 + n / 2
  res <- ye - as.numeric(Xd %*% mun)
  dm <- mun - mu0
  bn <- b0 + (sum(res^2) + as.numeric(t(dm) %*% L0 %*% dm)) / 2
  # Pulse's algebraically equal form.
  bn_alt <- b0 + (sum(ye^2) + as.numeric(t(mu0) %*% L0 %*% mu0) -
    as.numeric(t(mun) %*% Ln %*% mun)) / 2
  bl_near(bn, bn_alt, 1e-12, paste(name, "b_n residual vs quadratic form"))

  # Route 2: least squares on the augmented data, by QR.
  Xa <- rbind(Xd, sqrt(eps) * diag(q))
  ya <- c(ye, sqrt(eps) * mu0)
  fa <- lm.fit(Xa, ya)
  bl_near(mun, fa$coefficients, 1e-10, paste(name, "mu_n vs augmented QR"))
  bl_near(bn - b0, sum(fa$residuals^2) / 2, 1e-10, paste(name, "b_n vs augmented RSS"))
  Rq <- qr.R(fa$qr)
  piv <- fa$qr$pivot
  stopifnot(all(piv == seq_len(q)))
  bl_near(Lninv, chol2inv(Rq), 1e-10, paste(name, "Lambda_n^-1 vs QR"))

  # Direct weighted algebra (X'WX) equals the expansion.
  if (any(w != 1)) {
    Xw <- cbind(1, X)
    Lw <- L0 + crossprod(Xw, Xw * w)
    muw <- as.numeric(solve(Lw, L0 %*% mu0 + crossprod(Xw, y * w)))
    bl_near(mun, muw, 1e-12, paste(name, "weighted mu_n vs expansion"))
  }

  df <- 2 * an
  s2 <- bn / an
  se <- sqrt(s2 * diag(Lninv))
  tq <- function(lv) {
    p <- (1 + lv) / 2
    t <- qt(p, df)
    bl_near(pt(t, df), p, 1e-14, paste(name, "qt round trip"))
    t
  }
  tc <- tq(cred)
  tl <- tq(level)
  rss <- sum(res^2)
  tss <- sum((ye - mean(ye))^2)
  r2 <- 1 - rss / tss
  adj <- 1 - (1 - r2) * (n - 1) / (n - q)
  stopifnot(an > 1)
  vc <- bn / (an - 1) * Lninv

  out <- list(
    name = name, predictors = I(colnames(X)), weighted = any(w != 1),
    prior = list(mu0 = bl_vec(mu0), precision = bl_num(eps), shape = bl_num(a0), rate = bl_num(b0)),
    prior_set = list(mu0 = !is.null(prior$mu0), precision = !is.null(prior$eps),
                     shape = !is.null(prior$a0), rate = !is.null(prior$b0)),
    credible_level = bl_num(cred), credible_set = !is.null(credible),
    conf_level = bl_num(level), conf_set = !is.null(conf),
    rows = lapply(seq_along(y), function(i) bl_vec(c(X[i, ], y[i], w[i]))),
    n_obs = bl_num(n), a_n = bl_num(an), b_n = bl_num(bn), df = bl_num(df),
    coefficients = bl_vec(mun), std_errors = bl_vec(se),
    credible_lower = bl_vec(mun - tc * se), credible_upper = bl_vec(mun + tc * se),
    ci_lower = bl_vec(mun - tl * se), ci_upper = bl_vec(mun + tl * se),
    residual_std_err = bl_num(sqrt(s2)),
    r2 = bl_num(r2), adj_r2 = bl_num(adj),
    vcov = bl_mat(vc))
  bl_cases[[length(bl_cases) + 1]] <<- out
}

# Default prior (eps = a0 = b0 = 1e-3, mu0 = 0): two predictors, noisy.
i <- 0:39
X2 <- cbind(x1 = 4 * sin(0.31 * i), x2 = cos(0.17 * i) + i / 20)
bl_case("default_prior_two_predictors", X2,
  2.5 + 0.9 * X2[, "x1"] - 1.4 * X2[, "x2"] + 1.2 * sin(1.9 * i + 0.4))

# An informative prior: mu0 away from the data, a precision that pulls
# the posterior visibly, a proper InvGamma(3, 4), a 90% credible level
# and an 80% conf_level (the CI and credible interval differ).
i <- 0:24
X1 <- cbind(x = cos(0.7 * i + 0.3) * 2)
bl_case("informative_prior_levels", X1,
  1.0 + 0.6 * X1[, "x"] + 0.9 * sin(2.1 * i + 0.8),
  prior = list(mu0 = c(3, -1), eps = 2.5, a0 = 3, b0 = 4),
  credible = 0.90, conf = 0.80)

# Small n: five rows, two predictors — 2 a_n ≈ 5, far from the normal.
bl_case("small_n_two_predictors",
  cbind(x1 = c(1, 2, 3, 4, 5), x2 = c(2.5, 1.0, 4.0, 3.5, 6.0)),
  c(2.1, 2.9, 5.2, 5.1, 8.3))

# Three predictors, one null slope, conf_level only (credible default).
i <- 0:59
X3 <- cbind(x1 = 5 * sin(0.37 * i), x2 = cos(0.21 * i) + i / 30, x3 = (i %% 7) - 3)
bl_case("three_predictors_conf_90", X3,
  1.2 + 0.8 * X3[, "x1"] - 0.6 * X3[, "x2"] + 1.5 * sin(1.7 * i + 0.3),
  conf = 0.90)

# Frequency weights 1..3: the posterior on the rep() expansion.
i <- 0:29
Xw <- cbind(x1 = sin(0.5 * i) * 2, x2 = (i %% 4) - 1.5)
yw <- 0.7 + 1.3 * Xw[, "x1"] - 0.4 * Xw[, "x2"] + 0.8 * cos(1.9 * i)
bl_case("frequency_weighted_default_prior", Xw, yw, w = 1 + (i %% 3))

# Frequency weights under an informative prior (mu0, eps, a0, b0 all set).
bl_case("frequency_weighted_informative_prior", Xw, yw, w = 1 + (i %% 3),
  prior = list(mu0 = c(0.5, 1, 0), eps = 0.75, a0 = 2, b0 = 1.5),
  credible = 0.99)

bl_doc <- list(
  generator = "scripts/reference/gen_bayes_linear.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_BAYES_LINEAR: Normal-Inverse-Gamma conjugate posterior (mean, marginal t scale, credible / conf intervals, df, sigma scale, R², adjusted R², posterior covariance)",
  r_function = "Lambda_n <- eps*I + crossprod(X); mu_n <- solve(Lambda_n, eps*mu0 + crossprod(X, y)); a_n <- a0 + n/2; b_n <- b0 + (sum((y - X mu_n)^2) + eps*sum((mu_n - mu0)^2))/2; se <- sqrt(b_n/a_n*diag(solve(Lambda_n))); mu_n -/+ qt((1+level)/2, 2*a_n)*se; vcov <- b_n/(a_n-1)*solve(Lambda_n); self-checked against lm.fit on [X; sqrt(eps) I], [y; sqrt(eps) mu0]",
  note = paste(
    "rows are [predictors..., y, w]; a case is the posterior on the rep()-expansion by w (w = 1 unweighted).",
    "Vectors are ordered (intercept), then predictors; vcov is row-major in that order.",
    "prior_set / credible_set / conf_set say which knobs the case passes explicitly (an unset one takes Pulse's default: mu0 = 0, precision = shape = rate = 1e-3, credible_level 0.95, conf_level = credible_level)."),
  cases = bl_cases)
write_golden("bayes_linear", bl_doc)
