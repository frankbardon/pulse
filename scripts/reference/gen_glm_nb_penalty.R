# gen_glm_nb_penalty.R — R oracle for the penalized REG_GLM negative
# binomial family (U69 E3-S1, #638): l2 / l1 / elasticnet with theta
# re-estimated by maximum likelihood around the penalized fit, or fixed,
# as mpath::glmregNB fits it, under glmnet's objective (standardize =
# FALSE, intercept unpenalized, N* = sum(w)):
#
#   -loglik(b; theta)/N* + alpha * [(1 - l1_ratio)/2 * ||b||^2 + l1_ratio * ||b||_1]
#
# Writes <out_dir>/glm_nb_penalty.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   R_LIBS=<lib with mpath 0.4-2.28> Rscript scripts/reference/gen_glm_nb_penalty.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned packages: mpath 0.4-2.28 (CRAN; compiles Fortran, so install it
# with the Homebrew FLIBS workaround in regenerating-goldens.md), its
# imports pscl 1.5.9, bst 0.3-24, WeightSVM 1.7-16, doParallel 1.0.17
# (recorded, not called), glmnet 5.1 (its Depends), MASS 7.3-66
# (theta.ml, which glmregNB calls), jsonlite.
#
# glmregNB alternates its penalized IRLS / coordinate descent
# (glmreg_fit, family "negbin" at theta) with theta <- MASS::theta.ml(y,
# mu, sum(w), w) at the fitted means, from glm.nb(y ~ 1)'s mu and theta.
# Penalty scaling, mpath -> Pulse: with standardize = FALSE glmreg
# minimizes -sum(w loglik) + lambda * P (NOT divided by sum(w)), P =
# sum_j pf_j [alpha_m |b_j| + (1 - alpha_m)/2 b_j^2], and rescales
# penalty.factor to sum to nvars as glmnet does — so Pulse's
# (alpha, l1_ratio, unpenalized) is mpath's lambda = alpha * N* *
# sum(pf) / nvars, alpha_m = l1_ratio (l2 0, l1 1), pf 0 on the
# unpenalized predictors. thresh = 1e-14, maxit = 1e6 and maxit.theta =
# 1000 push both loops far past mpath's defaults (thresh 1e-3,
# maxit.theta 10), so the alternation stops on its own rule
# |dLoglik|/d1 + |dtheta| < 1e-8 at a fixed point; parallel = FALSE (one
# lambda).
#
# Self-checks before anything is written (stop on failure): the KKT
# conditions of the objective ABOVE (written out here, not mpath's) at
# mpath's (b, theta) at relative 1e-9 — the intercept's score is zero, a
# nonzero coefficient's penalized score is zero, a zero coefficient's
# score lies inside +-alpha.l1_ratio.pf — with the NB score
# sum w (y - mu) theta / (theta + mu) x / N* (log link); and, theta
# estimated, the theta score at mpath's mu within 1e-8 of its scale. So
# the fixture pins the stated joint stationary point, not an mpath
# convention.
#
# Recorded per case: coefficients (b0, then the predictors), zero, theta,
# theta_std_err (l2 + theta estimated: 1 / sqrt(info(theta-hat)) at the
# penalized mu, theta.ml's SE; otherwise null — Pulse reports none on a
# fixed theta and none on an l1 / elasticnet fit), std_errors (l2: the
# penalized-Fisher sandwich H^-1 A H^-1 at theta-hat, A = X' diag(w W) X,
# W = mu theta / (theta + mu), H = A + N*.alpha.(1 - l1_ratio).diag(0,
# pf); l1 / elasticnet null), deviance (the NB deviance at theta-hat),
# and, unweighted, hat — the leverage of the penalized IRLS's final
# weighted least squares, h = W x' H_S^-1 x over S = the intercept and
# the hat's active predictors (every one under l2, the nonzero ones
# under l1 / elasticnet; H_S's ridge term alpha.(1 - l1_ratio).N*.pf).
#
# Seeded (set.seed(20261124)); sourced last in gen_reference.R, so no
# earlier golden moves. Rows are [x1, x2, x3, y, w].

suppressPackageStartupMessages({
  library(jsonlite)
  library(MASS)
  library(mpath)
})

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE, null = "null")
    cat(txt, file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_glm_nb_penalty.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("mpath")) != "0.4.2.28") stop("gen_glm_nb_penalty.R pins mpath 0.4-2.28, got ", packageVersion("mpath"))
if (as.character(packageVersion("glmnet")) != "5.1") stop("gen_glm_nb_penalty.R pins glmnet 5.1, got ", packageVersion("glmnet"))
if (as.character(packageVersion("MASS")) != "7.3.66") stop("gen_glm_nb_penalty.R pins MASS 7.3-66, got ", packageVersion("MASS"))

np_num <- function(x) {
  if (length(x) != 1) stop("np_num() takes a scalar")
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
np_vec <- function(v) lapply(unname(as.numeric(v)), np_num)

np_score_theta <- function(th, mu, y, w) sum(w * (digamma(th + y) - digamma(th) + log(th) + 1 - log(th + mu) - (y + th) / (mu + th)))
np_info_theta <- function(th, mu, y, w) sum(w * (-trigamma(th + y) + trigamma(th) - 1 / th + 2 / (mu + th) - (y + th) / (mu + th)^2))

np_cases <- list()

# np_case fits one penalized NB model on X (n x p, named columns), y, w
# through glmregNB, self-checks it, and records it. theta: NULL
# estimates it, a number fixes it.
np_case <- function(name, X, y, w, penalty, alpha, l1_ratio = 0, unpenalized = character(), theta = NULL) {
  mix <- switch(penalty, l2 = 0, l1 = 1, elasticnet = l1_ratio)
  stopifnot(all(unpenalized %in% colnames(X)))
  p <- ncol(X)
  pf <- ifelse(colnames(X) %in% unpenalized, 0, 1)
  nstar <- sum(w)
  d <- data.frame(X, y = y, .w = w)
  fml <- as.formula(paste("y ~", paste(colnames(X), collapse = " + ")))
  lam <- alpha * nstar * sum(pf) / p
  args <- list(formula = fml, data = d, weights = d$.w, lambda = lam, alpha = mix,
               standardize = FALSE, penalty.factor = pf, thresh = 1e-14, maxit = 1e6,
               maxit.theta = 1000, parallel = FALSE)
  if (!is.null(theta)) args <- c(args, list(nlambda = 1, theta.fixed = TRUE, theta0 = theta))
  fit <- suppressWarnings(do.call(glmregNB, args))
  if (!isTRUE(as.logical(fit$converged))) stop(name, ": glmregNB did not converge")
  b <- c(as.numeric(fit$b0), as.numeric(fit$beta))
  th <- as.numeric(fit$theta)
  if (!is.null(theta) && th != theta) stop(name, ": fixed theta moved")
  Xd <- cbind(1, X)
  mu <- exp(as.numeric(Xd %*% b))
  # KKT of -sum(w l)/N* + alpha * P at theta-hat.
  score <- as.numeric(t(Xd) %*% (w * (y - mu) * th / (th + mu))) / nstar
  l1 <- alpha * mix
  l2 <- alpha * (1 - mix)
  scale <- max(1, max(abs(score)))
  if (abs(score[1]) > 1e-9 * scale) stop(name, ": intercept score ", score[1])
  for (j in seq_along(b)[-1]) {
    f <- pf[j - 1]
    if (b[j] != 0) {
      r <- score[j] - l2 * f * b[j] - l1 * f * sign(b[j])
      if (abs(r) > 1e-9 * scale) stop(name, ": KKT active ", j, " residual ", r)
    } else if (abs(score[j]) > l1 * f * (1 + 1e-9) + 1e-14) {
      stop(name, ": KKT inactive ", j, " |score| ", abs(score[j]), " > ", l1 * f)
    }
  }
  info <- np_info_theta(th, mu, y, w)
  if (is.null(theta)) {
    st <- np_score_theta(th, mu, y, w)
    if (abs(st) > 1e-8 * max(1, abs(info) * th)) stop(name, ": theta score ", st)
    thml <- theta.ml(y, mu, nstar, w, limit = 100, eps = 1e-12 * th)
    if (abs(thml - th) > 1e-8 * th) stop(name, ": theta.ml at mpath's mu ", thml, " vs ", th)
  }
  W <- mu * th / (th + mu)
  se <- rep(NA_real_, length(b))
  if (penalty == "l2") {
    A <- t(Xd) %*% (Xd * (w * W))
    H <- A + diag(c(0, nstar * l2 * pf), length(b))
    Hi <- solve(H)
    se <- sqrt(diag(Hi %*% A %*% Hi))
  }
  tse <- if (is.null(theta) && penalty == "l2") 1 / sqrt(info) else NA_real_
  dev <- 2 * sum(w * (ifelse(y > 0, y * log(y / mu), 0) - (y + th) * log((y + th) / (mu + th))))
  doc <- list(
    name = name, family = "negative_binomial", link = "log", penalty = penalty,
    alpha = np_num(alpha), l1_ratio = np_num(if (penalty == "elasticnet") l1_ratio else 0),
    theta_fixed = np_num(if (is.null(theta)) 0 else theta),
    predictors = I(colnames(X)), weighted = any(w != 1),
    columns = I(c(colnames(X), "y", "w")),
    rows = lapply(seq_len(nrow(X)), function(i) np_vec(c(X[i, ], y[i], w[i]))),
    coefficients = np_vec(b), std_errors = np_vec(se),
    zero = I(colnames(X)[b[-1] == 0]),
    theta = np_num(th), theta_std_err = np_num(tse),
    deviance = np_num(dev))
  if (all(w == 1)) {
    S <- c(1, 1 + which(penalty == "l2" | b[-1] != 0))
    HS <- t(Xd[, S, drop = FALSE]) %*% (Xd[, S, drop = FALSE] * W)
    ridge <- c(0, pf)[S] * l2 * nstar
    HS <- HS + diag(ridge, length(S))
    HSi <- solve(HS)
    doc$hat <- np_vec(W * rowSums((Xd[, S, drop = FALSE] %*% HSi) * Xd[, S, drop = FALSE]))
  }
  if (length(unpenalized) > 0) doc$unpenalized <- I(unpenalized)
  np_cases[[length(np_cases) + 1]] <<- doc
}

set.seed(20261124)
n <- 200
x1 <- round(rnorm(n), 4)
x2 <- round(runif(n, 0, 4), 4)
x3 <- round(rnorm(n), 4)
y <- rnbinom(n, size = 2.5, mu = exp(0.5 + 0.4 * x1 + 0.2 * x2))
X <- cbind(x1 = x1, x2 = x2, x3 = x3)
ones <- rep(1, n)
wf <- rep(c(1, 2, 3, 1), length.out = n)

# x3 carries no signal, so the l1 / elasticnet fits zero it; unpenalized
# keeps it in, unshrunk.
np_case("nb_l2", X, y, ones, "l2", 0.05)
np_case("nb_l1", X, y, ones, "l1", 0.2)
np_case("nb_elasticnet", X, y, ones, "elasticnet", 0.25, 0.5)
np_case("nb_l1_unpenalized", X, y, ones, "l1", 0.2, unpenalized = "x3")
np_case("nb_l2_unpenalized", X, y, ones, "l2", 0.05, unpenalized = "x1")
np_case("nb_elasticnet_weighted", X, y, wf, "elasticnet", 0.25, 0.5)
np_case("nb_l2_weighted_unpenalized", X, y, wf, "l2", 0.05, unpenalized = c("x1", "x2"))
np_case("nb_l1_fixed_theta", X, y, ones, "l1", 0.2, theta = 2)
np_case("nb_l2_fixed_theta", X, y, ones, "l2", 0.05, theta = 1.5)

np_doc <- list(
  generator = "scripts/reference/gen_glm_nb_penalty.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(mpath = as.character(packageVersion("mpath")),
                  glmnet = as.character(packageVersion("glmnet")),
                  MASS = as.character(packageVersion("MASS")),
                  pscl = as.character(packageVersion("pscl")),
                  bst = as.character(packageVersion("bst")),
                  WeightSVM = as.character(packageVersion("WeightSVM")),
                  doParallel = as.character(packageVersion("doParallel")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_GLM negative_binomial penalty l2 / l1 / elasticnet: theta re-estimated around the penalized fit (or fixed), glmnet's objective, unstandardised, intercept unpenalised",
  r_function = "mpath::glmregNB(y ~ ., weights = w, lambda = alpha * N* * sum(pf) / nvars, alpha = l1_ratio, standardize = FALSE, penalty.factor = pf, thresh = 1e-14, maxit = 1e6, maxit.theta = 1000, parallel = FALSE[, nlambda = 1, theta.fixed = TRUE, theta0 = theta])",
  note = paste(
    "Pulse alpha = mpath lambda / N* (standardize = FALSE: mpath does not divide the loglik by sum(w)), times nvars / sum(pf) under unpenalized; Pulse l1_ratio = mpath alpha (l2 0, l1 1).",
    "rows are [x1, x2, x3, y, w]; N* = sum(w); w a frequency weight.",
    "theta_fixed 0 = theta estimated (theta.ml at the penalized means), else the fixed theta.",
    "std_errors: l2 the penalised-Fisher sandwich H^-1 A H^-1 at theta-hat; l1 / elasticnet null.",
    "theta_std_err: l2 with theta estimated 1/sqrt(info(theta-hat)); otherwise null.",
    "hat (unweighted cases): W x' H_S^-1 x over the intercept and the hat's active predictors.",
    "deviance: the NB deviance at theta-hat and the penalised estimate.",
    "Every case is self-checked against the KKT conditions of the stated objective at relative 1e-9 and, theta estimated, theta.ml at mpath's means."),
  cases = np_cases)

write_golden("glm_nb_penalty", np_doc)
