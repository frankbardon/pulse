# gen_ols_lm.R — R oracle for unpenalised REG_OLS (reference-oracles
# E2-S1, #203): coefficients, standard errors, t, p, the 95% interval,
# R², adjusted R², σ̂ and the residual df, all from stock lm().
#
# Writes <out_dir>/ols_lm.json. Sourced from gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_ols_lm.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer). CI never runs R. Base stats
# + jsonlite only.
#
# lm() solves by Householder QR on the design; Pulse folds centered
# Welford moments and Cholesky-solves the centered Gram, so the two meet
# only to rounding — every figure is pinned at relative 1e-10. Each case
# is self-checked here against the normal-equations closed form on the
# centered design (solve(crossprod(Xc)), RSS / df) at relative 1e-10, so
# the golden pins lm's definition, not a QR artefact.
#
# large_offset_predictor (x = 1e6 + 0.5 i) is where stock lm() itself is
# the less accurate side: QR on the raw column loses ~eps * 1e6 / sd(x)
# ≈ 1e-11 relative, which its adjusted R² (≈ -0.001, a cancellation of
# 1 - (1 - R²)(n - 1)/df) amplifies to 1.1e-9 — beyond the 1e-10 pin.
# The case therefore passes `shift` = 1e6: lm() runs on x - 1e6 = 0.5 i
# (an EXACT subtraction, so the same data), the slopes, SEs, R², σ̂ are
# that fit's, and the raw-coordinate intercept and its variance are
# mapped back exactly (b0 = b0' - shift·b, Var = A V Aᵀ, every term of
# which is positive here — no cancellation). Stock lm()'s raw-coordinate
# figures stay beside it (r_coefficients, r_std_errors, r_p_values,
# r_r2, r_adj_r2) as the cross-check, asserted within 1e-8.
#
# frequency_weighted is the weighted row: integer frequency weights mean
# exactly "this row n times", so its reference is lm() on the rep()-
# EXPANDED rows, run unweighted (NOT lm(weights =), whose residual df is
# the row count). Its rows carry w; the unweighted cases carry w = 1.
#
# The rows are computed here and stored at %.17g; the Go test fits the
# stored rows, so the two never need to agree on sin().

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

ol_num <- function(x) {
  if (length(x) != 1) stop("ol_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
ol_vec <- function(v) lapply(unname(as.numeric(v)), ol_num)
ol_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (!all(abs(a - b) <= tol * pmax(abs(a), abs(b)))) {
    stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / pmax(abs(a), abs(b))))
  }
}

ol_cases <- list()

# ol_case fits y ~ X by lm on the rep()-expansion of the rows (w = 1
# everywhere for an unweighted case) and records lm's figures.
ol_case <- function(name, X, y, w = rep(1, length(y)), conf = 0.95, shift = NULL) {
  stopifnot(all(w == round(w)), all(w >= 1))
  idx <- rep(seq_along(y), w)
  Xe <- X[idx, , drop = FALSE]
  ye <- y[idx]
  fit_on <- function(Xf) {
    d <- data.frame(Xf, y = ye)
    lm(reformulate(colnames(X), "y"), data = d)
  }
  raw <- fit_on(Xe)
  fit <- raw
  if (!is.null(shift)) {
    Xs <- sweep(Xe, 2, shift)
    if (!all(Xs + rep(shift, each = nrow(Xs)) == Xe)) stop(name, ": shift is not exact")
    fit <- fit_on(Xs)
  }
  s <- summary(fit)
  df <- fit$df.residual
  # Map the (possibly shifted) fit back to raw coordinates: b = A b',
  # Var(b) = A Var(b') Aᵀ, A the identity but A[1, j + 1] = -shift_j.
  A <- diag(ncol(X) + 1)
  if (!is.null(shift)) A[1, -1] <- -shift
  b <- as.numeric(A %*% coef(fit))
  se <- sqrt(diag(A %*% vcov(fit) %*% t(A)))
  tv <- b / se
  pv <- 2 * pt(-abs(tv), df)
  q <- qt(1 - (1 - conf) / 2, df)
  lo <- b - q * se
  hi <- b + q * se
  # Unshifted, the figures ARE summary(lm) / confint(lm).
  if (is.null(shift)) {
    ci <- confint(fit, level = conf)
    cf <- s$coefficients
    ol_near(b, cf[, 1], 0, paste(name, "identity map"))
    ol_near(se, cf[, 2], 1e-15, paste(name, "vcov vs summary SE"))
    ol_near(pv, cf[, 4], 1e-12, paste(name, "summary p vs 2*pt"))
    ol_near(c(lo, hi), c(ci[, 1], ci[, 2]), 1e-14, paste(name, "confint"))
  }
  # Closed-form self-check: the normal equations on the CENTERED
  # expansion (the raw Gram of large_offset_predictor is singular to
  # working precision), intercept and its variance recovered from the
  # means.
  n <- nrow(Xe)
  mx <- colMeans(Xe)
  Xc <- sweep(Xe, 2, mx)
  Si <- solve(crossprod(Xc))
  bs <- as.numeric(Si %*% crossprod(Xc, ye - mean(ye)))
  bcf <- c(mean(ye) - sum(mx * bs), bs)
  if (n - ncol(Xe) - 1 != df) stop(name, ": df mismatch")
  rss <- sum((ye - bcf[1] - Xe %*% bs)^2)
  secf <- sqrt(rss / df * c(1 / n + as.numeric(t(mx) %*% Si %*% mx), diag(Si)))
  ol_near(b, bcf, 1e-10, paste(name, "lm vs normal-equation coefficients"))
  ol_near(se, secf, 1e-10, paste(name, "lm vs closed-form std errors"))
  ol_near(s$sigma, sqrt(rss / df), 1e-10, paste(name, "lm sigma vs closed form"))
  out <- list(
    name = name, predictors = I(colnames(X)), weighted = any(w != 1),
    conf_level = ol_num(conf),
    rows = lapply(seq_along(y), function(i) ol_vec(c(X[i, ], y[i], w[i]))),
    n_obs = ol_num(n), df = ol_num(df),
    coefficients = ol_vec(b), std_errors = ol_vec(se),
    t_values = ol_vec(tv), p_values = ol_vec(pv),
    ci_lower = ol_vec(lo), ci_upper = ol_vec(hi),
    r2 = ol_num(s$r.squared), adj_r2 = ol_num(s$adj.r.squared),
    residual_std_err = ol_num(s$sigma))
  if (!is.null(shift)) {
    rs <- summary(raw)
    ol_near(rs$coefficients[, 1], b, 1e-8, paste(name, "stock lm coefficients"))
    ol_near(rs$coefficients[, 2], se, 1e-8, paste(name, "stock lm SE"))
    ol_near(rs$adj.r.squared, s$adj.r.squared, 1e-8, paste(name, "stock lm adj R²"))
    out$shift <- ol_vec(shift)
    out$r_coefficients <- ol_vec(rs$coefficients[, 1])
    out$r_std_errors <- ol_vec(rs$coefficients[, 2])
    out$r_p_values <- ol_vec(rs$coefficients[, 4])
    out$r_r2 <- ol_num(rs$r.squared)
    out$r_adj_r2 <- ol_num(rs$adj.r.squared)
  }
  ol_cases[[length(ol_cases) + 1]] <<- out
}

# The fixture TestRegOLS_GoldenRequest used to snapshot (Pulse's own
# output, now replaced by this lm() row): a near-exact linear signal with
# a small periodic disturbance — R² 0.9998, p down to ~1e-70.
i <- 0:39
ol_case("near_exact_two_predictors",
  cbind(x1 = i, x2 = (i %% 5) - 2),
  3 + 0.5 * i - 1.5 * ((i %% 5) - 2) + 0.1 * (i %% 3))

# A noisy three-predictor fit: one strong, one moderate, one null slope,
# so p spans ~1e-20 to ~0.5.
i <- 0:59
X3 <- cbind(x1 = 5 * sin(0.37 * i), x2 = cos(0.21 * i) + i / 30, x3 = (i %% 7) - 3)
ol_case("noisy_three_predictors", X3,
  1.2 + 0.8 * X3[, "x1"] - 0.6 * X3[, "x2"] + 0.0 * X3[, "x3"] + 1.5 * sin(1.7 * i + 0.3))

# One predictor, a weak slope (p ~ 0.1 region).
i <- 0:24
x <- cos(0.9 * i + 0.2) * 3
ol_case("single_weak_predictor", cbind(x = x), 10 + 0.15 * x + 1.1 * sin(2.3 * i + 1.1))

# Five rows, two predictors: residual df 2, so the t reference (and the
# interval's qt) sits far from the normal.
ol_case("small_df_two",
  cbind(x1 = c(1, 2, 3, 4, 5), x2 = c(2.5, 1.0, 4.0, 3.5, 6.0)),
  c(2.1, 2.9, 5.2, 5.1, 8.3))

# A predictor riding a large offset (1e6 + 0.5 i): the centered moments
# must not lose the slope to cancellation. Fitted on the exact shift
# x - 1e6 (header); stock lm()'s raw fit kept as r_*.
i <- 0:29
xo <- 1e6 + 0.5 * i
ol_case("large_offset_predictor", cbind(x = xo),
  -4 + 2e-3 * (xo - 1e6) * 7 + 0.3 * sin(1.3 * i), shift = 1e6)

# Frequency weights 1..3: lm on the rep() expansion (header).
i <- 0:29
Xw <- cbind(x1 = sin(0.5 * i) * 2, x2 = (i %% 4) - 1.5)
ol_case("frequency_weighted", Xw,
  0.7 + 1.3 * Xw[, "x1"] - 0.4 * Xw[, "x2"] + 0.8 * cos(1.9 * i),
  w = 1 + (i %% 3))

# A 90% interval, so conf_level reaches qt.
i <- 0:19
x9 <- (i - 9.5) / 3
ol_case("conf_level_90", cbind(x = x9), 4 - 0.9 * x9 + 0.6 * sin(2.9 * i), conf = 0.90)

ol_doc <- list(
  generator = "scripts/reference/gen_ols_lm.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_OLS unpenalised: coefficients, SE, t, p, confint, R², adjusted R², sigma, df",
  r_function = "fit <- lm(y ~ ., data); summary(fit)$coefficients; confint(fit, level); summary(fit)$r.squared / adj.r.squared / sigma; fit$df.residual (large_offset_predictor: lm on the exact shift x - shift, mapped back; stock lm as r_*)",
  note = paste(
    "rows are [predictors..., y, w]; a case is lm() on the rep()-expansion by w (w = 1 unweighted), the definition of frequency weights.",
    "coefficients / std_errors / t_values / p_values / ci_* are ordered (intercept), then predictors.",
    "Every case is self-checked against the normal-equation closed form at relative 1e-10.",
    "A case with `shift` was fitted on X - shift (exact) because stock lm()'s QR on the raw offset column misses 1e-10 (adjusted R² off 1.1e-9); r_* hold stock lm()'s raw figures."),
  cases = ol_cases)
write_golden("ols_lm", ol_doc)
