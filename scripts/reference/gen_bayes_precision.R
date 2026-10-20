# gen_bayes_precision.R — R oracle for REG_BAYES_LINEAR's per-coefficient
# prior precision (custom-regressions E3-S1, FR-30): prior_precision as a
# vector v, Lambda0 = diag(v), intercept first like prior_mu. The
# conjugate Normal-Inverse-Gamma posterior is the same closed form
# gen_bayes_linear.R writes out, with the diagonal prior:
#
#   Lambda_n = diag(v) + X'X                 (X with the intercept column)
#   mu_n     = Lambda_n^-1 (diag(v) mu0 + X'y)
#   a_n      = a0 + n / 2
#   b_n      = b0 + (|y - X mu_n|^2 + (mu_n - mu0)' diag(v) (mu_n - mu0)) / 2
#
# An entry v_j = 0 is a flat prior on beta_j (proper while Lambda_n stays
# positive-definite); a large v_j pins beta_j to mu0_j.
#
# Writes <out_dir>/bayes_precision.json. Sourced from gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_bayes_precision.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer). CI never runs R. Base stats
# + jsonlite only; no stock R package fits this prior.
#
# Self-checks, every case: b_n's residual form equals Pulse's quadratic
# form b0 + (y'y + mu0'L0 mu0 - mu_n'L_n mu_n)/2 at 1e-12; mu_n, b_n and
# Lambda_n^-1 equal least squares on the AUGMENTED data
# [X; diag(sqrt(v))], [y; sqrt(v) mu0] (lm.fit, Householder QR — a flat
# entry adds a zero row) at 1e-10; frequency-weighted cases equal the
# direct X'WX form at 1e-12.
#
# Emitted figures: coefficients mu_n, std_errors sqrt(b_n/a_n diag(L_n^-1)),
# credible_lower / credible_upper at credible_level 0.95 on t_{2 a_n},
# df = 2 a_n, residual_std_err sqrt(b_n/a_n), r2 / adj_r2, vcov
# b_n/(a_n - 1) L_n^-1. Rows are deterministic (no RNG), stored at %.17g;
# the Go test fits the stored rows.

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

bp_num <- function(x) {
  if (length(x) != 1) stop("bp_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
bp_vec <- function(v) lapply(unname(as.numeric(v)), bp_num)
bp_mat <- function(m) lapply(seq_len(nrow(m)), function(i) bp_vec(m[i, ]))
bp_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  scale <- pmax(abs(a), abs(b))
  ok <- abs(a - b) <= tol * scale | scale == 0
  if (!all(ok)) stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / scale))
}

bp_cases <- list()

# bp_case: the NIG posterior under Lambda0 = diag(v) on the rep()-expansion
# of the rows by w. mu0 NULL = zero vector; a0 = b0 = 1e-3 (Pulse's
# defaults); credible level 0.95 (the default).
bp_case <- function(name, X, y, v, mu0 = NULL, w = rep(1, length(y))) {
  stopifnot(all(w == round(w)), all(w >= 1))
  q <- ncol(X) + 1
  stopifnot(length(v) == q, all(v >= 0))
  mu0_set <- !is.null(mu0)
  if (is.null(mu0)) mu0 <- rep(0, q)
  a0 <- 1e-3
  b0 <- 1e-3
  cred <- 0.95

  idx <- rep(seq_along(y), w)
  Xd <- cbind(1, X[idx, , drop = FALSE])
  ye <- y[idx]
  n <- length(ye)

  L0 <- diag(v, q)
  Ln <- L0 + crossprod(Xd)
  Lninv <- solve(Ln)
  mun <- as.numeric(Lninv %*% (L0 %*% mu0 + crossprod(Xd, ye)))
  an <- a0 + n / 2
  res <- ye - as.numeric(Xd %*% mun)
  dm <- mun - mu0
  bn <- b0 + (sum(res^2) + as.numeric(t(dm) %*% L0 %*% dm)) / 2
  bn_alt <- b0 + (sum(ye^2) + as.numeric(t(mu0) %*% L0 %*% mu0) -
    as.numeric(t(mun) %*% Ln %*% mun)) / 2
  bp_near(bn, bn_alt, 1e-12, paste(name, "b_n residual vs quadratic form"))

  Xa <- rbind(Xd, diag(sqrt(v), q))
  ya <- c(ye, sqrt(v) * mu0)
  fa <- lm.fit(Xa, ya)
  bp_near(mun, fa$coefficients, 1e-10, paste(name, "mu_n vs augmented QR"))
  bp_near(bn - b0, sum(fa$residuals^2) / 2, 1e-10, paste(name, "b_n vs augmented RSS"))
  stopifnot(all(fa$qr$pivot == seq_len(q)))
  bp_near(Lninv, chol2inv(qr.R(fa$qr)), 1e-10, paste(name, "Lambda_n^-1 vs QR"))

  if (any(w != 1)) {
    Xw <- cbind(1, X)
    Lw <- L0 + crossprod(Xw, Xw * w)
    muw <- as.numeric(solve(Lw, L0 %*% mu0 + crossprod(Xw, y * w)))
    bp_near(mun, muw, 1e-12, paste(name, "weighted mu_n vs expansion"))
  }

  df <- 2 * an
  s2 <- bn / an
  se <- sqrt(s2 * diag(Lninv))
  tc <- qt((1 + cred) / 2, df)
  bp_near(pt(tc, df), (1 + cred) / 2, 1e-14, paste(name, "qt round trip"))
  rss <- sum(res^2)
  tss <- sum((ye - mean(ye))^2)
  r2 <- 1 - rss / tss
  adj <- 1 - (1 - r2) * (n - 1) / (n - q)
  stopifnot(an > 1)
  vc <- bn / (an - 1) * Lninv

  bp_cases[[length(bp_cases) + 1]] <<- list(
    name = name, predictors = I(colnames(X)), weighted = any(w != 1),
    precision = bp_vec(v), mu0 = bp_vec(mu0), mu0_set = mu0_set,
    rows = lapply(seq_along(y), function(i) bp_vec(c(X[i, ], y[i], w[i]))),
    n_obs = bp_num(n), a_n = bp_num(an), b_n = bp_num(bn), df = bp_num(df),
    coefficients = bp_vec(mun), std_errors = bp_vec(se),
    credible_lower = bp_vec(mun - tc * se), credible_upper = bp_vec(mun + tc * se),
    residual_std_err = bp_num(sqrt(s2)),
    r2 = bp_num(r2), adj_r2 = bp_num(adj),
    vcov = bp_mat(vc))
}

# Two media-style predictors. An experiment-centred prior on x1 (mu0 1.2,
# precision 40) and a near-diffuse one on x2; the intercept flat (0).
i <- 0:39
X2 <- cbind(x1 = 3 + 2 * sin(0.31 * i), x2 = cos(0.17 * i) + i / 20)
y2 <- 2.5 + 0.9 * X2[, "x1"] - 1.4 * X2[, "x2"] + 1.2 * sin(1.9 * i + 0.4)
bp_case("experiment_centred_x1_flat_intercept", X2, y2,
  v = c(0, 40, 1e-3), mu0 = c(0, 1.2, 0))

# Every entry flat: the posterior mean is the least-squares fit.
bp_case("all_flat", X2, y2, v = c(0, 0, 0))

# A strong precision on x2 pins it near its prior mean (-1). Pulse
# evaluates b_n by the quadratic form, which cancels mu0'L0 mu0 against
# mu_n'L_n mu_n: its relative error grows with the precision (~5e-12 at
# 1e6), so the case stays at 1e5, inside the 1e-12 self-check; the
# precision -> infinity limit itself is a Go property test.
bp_case("pinned_x2", X2, y2, v = c(1e-3, 1e-3, 1e5), mu0 = c(0, 0, -1))

# Three predictors, mixed precisions, default (zero) prior mean.
i <- 0:59
X3 <- cbind(x1 = 5 * sin(0.37 * i), x2 = cos(0.21 * i) + i / 30, x3 = (i %% 7) - 3)
bp_case("three_predictors_mixed", X3,
  1.2 + 0.8 * X3[, "x1"] - 0.6 * X3[, "x2"] + 1.5 * sin(1.7 * i + 0.3),
  v = c(0.5, 2, 0, 10))

# Frequency weights 1..3 under a vector prior: the rep() expansion.
i <- 0:29
Xw <- cbind(x1 = sin(0.5 * i) * 2, x2 = (i %% 4) - 1.5)
yw <- 0.7 + 1.3 * Xw[, "x1"] - 0.4 * Xw[, "x2"] + 0.8 * cos(1.9 * i)
bp_case("frequency_weighted_vector", Xw, yw,
  v = c(0, 3, 0.25), mu0 = c(0.5, 1, 0), w = 1 + (i %% 3))

bp_doc <- list(
  generator = "scripts/reference/gen_bayes_precision.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_BAYES_LINEAR with a per-coefficient prior_precision vector: Lambda0 = diag(v), the Normal-Inverse-Gamma conjugate posterior",
  r_function = "Lambda_n <- diag(v) + crossprod(X); mu_n <- solve(Lambda_n, diag(v) %*% mu0 + crossprod(X, y)); a_n <- a0 + n/2; b_n <- b0 + (sum((y - X mu_n)^2) + t(mu_n - mu0) %*% diag(v) %*% (mu_n - mu0))/2; self-checked against lm.fit on [X; diag(sqrt(v))], [y; sqrt(v) mu0]",
  note = paste(
    "rows are [predictors..., y, w]; a case is the posterior on the rep()-expansion by w (w = 1 unweighted).",
    "precision and mu0 are ordered (intercept), then predictors; prior_shape = prior_rate = 1e-3 and credible_level 0.95 (Pulse's defaults).",
    "mu0_set says whether the case passes prior_mu (unset = the zero vector)."),
  cases = bp_cases)
write_golden("bayes_precision", bp_doc)
