# gen_multinomial.R — R oracle for REG_MULTINOMIAL, the baseline-category
# (multinomial) logit (U65 E4-S2): nnet::multinom's fit pushed to the
# exact maximum-likelihood estimate.
#
# Writes <out_dir>/multinomial.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_multinomial.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. R 4.6.1 +
# jsonlite + nnet 7.3-21 (a recommended package this R build does not
# ship: install it into the user library from the Posit noble binary
# repo, docs/src/internals/regenerating-goldens.md, The R toolchain).
#
# The model, per row i with predictors x_i (intercept first) and L
# observed levels, the reference r:
#   eta_ik = x_i' beta_k (beta_r = 0),  pi_ik = exp(eta_ik) / sum_l exp(eta_il)
#   logLik = sum_i w_i log pi_{i, y_i}
# multinom(y ~ x1 + x2, Hess = TRUE, maxit = 1000, reltol = 1e-16) is the
# starting point; its BFGS stops a few 1e-8 off the MLE, so the script
# then takes Newton steps on the analytic gradient and Hessian
# (-d2 logLik = sum_i w_i (diag(pi_i) - pi_i pi_i') (x) x_i x_i') until
# the step is below 1e-15 relative — the MLE to double precision. The
# polished coefficients must agree with multinom's at 1e-6 and
# solve(Hessian) with vcov(multinom) at 1e-4 (multinom's Hessian is a
# finite difference of its gradient); both are asserted. Every figure is
# the polished fit's:
#   coefficients / std_errors / p_values / ci_*: category-major (each
#     non-reference level in level order, then (intercept), x1, x2) —
#     vcov(multinom)'s row order; SE = sqrt(diag(solve(H))), z = b / SE,
#     p = 2 pnorm(-|z|), ci = b -/+ qnorm(0.5 + conf/2) SE;
#   deviance = -2 logLik, null_deviance the intercept-only model's
#     (-2 sum_i w_i log(share of y_i)), two_log_lik = 2 logLik,
#     aic = deviance + 2 K (p + 1) (multinom's edf).
# Weights: "_freq" cases (integer f) are the rep()-expanded fit
# (asserted equal to multinom(weights = f)); "_prob" cases are
# multinom(weights = w*) with w* = w n_eff / sum(w), n_eff = (sum w)^2 /
# sum w^2 — REG_GLM's prior-weight rule — and carry no two_log_lik /
# aic (a pseudo-likelihood). A level with no row is dropped, as
# multinom drops it ("group is empty"); the reference defaults to the
# first observed level (multinom's) or is named (relevel()).
#
# Seeded (set.seed(20261102)). Rows are [x1, x2, y] or [x1, x2, y, w],
# y the 0-based index of the row's level in `levels` (the target's
# dictionary order).

suppressPackageStartupMessages({
  library(jsonlite)
  library(nnet)
})

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_multinomial.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("nnet")) != "7.3.21") stop("gen_multinomial.R pins nnet 7.3-21, got ", packageVersion("nnet"))

mn_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
mn_check <- function(what, got, want, tol) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  err <- max(abs(got - want) / pmax(1, abs(want)))
  if (err > tol) stop(what, ": ", err, " > ", tol)
  err
}

# mn_parts: per-row probabilities (n x L, reference column first in the
# fitted order), the weighted log-likelihood, the gradient and the
# negative Hessian of beta (K (p+1), category-major).
mn_parts <- function(beta, X, Yi, w) {
  k <- ncol(Yi) - 1; q <- ncol(X)
  B <- matrix(beta, nrow = q)               # q x K, column k = category k
  eta <- cbind(0, X %*% B)
  m <- apply(eta, 1, max)
  e <- exp(eta - m)
  P <- e / rowSums(e)
  ll <- sum(w * (rowSums(Yi * eta) - m - log(rowSums(e))))
  g <- as.vector(t(X) %*% (w * (Yi[, -1, drop = FALSE] - P[, -1, drop = FALSE])))
  H <- matrix(0, k * q, k * q)
  for (a in 1:k) for (b in 1:k) {
    s <- if (a == b) P[, a + 1] * (1 - P[, a + 1]) else -P[, a + 1] * P[, b + 1]
    H[(a - 1) * q + 1:q, (b - 1) * q + 1:q] <- t(X) %*% (X * (w * s))
  }
  list(P = P, ll = ll, g = g, H = H)
}

mn_cases <- list()

# mn_case fits y (a factor over `levels`, in that order) on x1 + x2.
mn_case <- function(name, d, levels, reference = NULL, weight = NULL, kind = NULL, conf = 0.95) {
  y <- factor(levels[d$y + 1], levels = levels)
  w <- rep(1, nrow(d))
  if (!is.null(weight)) {
    w <- weight
    if (kind == "probability") w <- w * (sum(w)^2 / sum(w^2)) / sum(w)
  }
  observed <- levels[levels %in% as.character(y)]
  ref <- if (is.null(reference)) observed[1] else reference
  fitted_levels <- c(ref, setdiff(observed, ref))
  yy <- factor(as.character(y), levels = fitted_levels)
  dd <- data.frame(x1 = d$x1, x2 = d$x2, yy = yy, w = w)
  m <- suppressWarnings(multinom(yy ~ x1 + x2, data = dd, weights = w, Hess = TRUE,
    maxit = 1000, reltol = 1e-16, trace = FALSE))
  X <- cbind(1, d$x1, d$x2)
  Yi <- sapply(fitted_levels, function(l) as.numeric(as.character(yy) == l))
  beta <- as.vector(t(coef(m)))
  for (it in 1:100) {
    pp <- mn_parts(beta, X, Yi, w)
    step <- solve(pp$H, pp$g)
    beta <- beta + step
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(beta)))) break
  }
  pp <- mn_parts(beta, X, Yi, w)
  if (max(abs(pp$g)) > 1e-9) stop(name, ": gradient not zero at the polished MLE")
  mn_check(paste(name, "multinom coefficients"), as.vector(t(coef(m))), beta, 1e-6)
  V <- solve(pp$H)
  mn_check(paste(name, "multinom vcov"), as.vector(vcov(m)), as.vector(V), 1e-4)
  if (!is.null(weight) && kind == "frequency") {
    ex <- dd[rep(seq_len(nrow(dd)), weight), ]; ex$w <- 1
    me <- suppressWarnings(multinom(yy ~ x1 + x2, data = ex, Hess = TRUE, maxit = 1000,
      reltol = 1e-16, trace = FALSE))
    mn_check(paste(name, "weighted vs expanded coefficients"), as.vector(t(coef(me))), beta, 1e-6)
    mn_check(paste(name, "weighted vs expanded deviance"), deviance(me), -2 * pp$ll, 1e-9)
  }
  se <- sqrt(diag(V))
  z <- beta / se
  q <- qnorm(0.5 + conf / 2)
  shares <- colSums(Yi * w) / sum(w)
  null_dev <- -2 * sum(w * (Yi %*% log(shares)))
  k <- length(fitted_levels) - 1
  keys <- as.vector(t(outer(fitted_levels[-1], c("(intercept)", "x1", "x2"), paste, sep = ":")))
  rows <- lapply(seq_len(nrow(d)), function(i) {
    r <- c(d$x1[i], d$x2[i], d$y[i])
    if (!is.null(weight)) r <- c(r, weight[i])
    mn_g17(r)
  })
  doc <- list(
    name = name, levels = levels, categories = fitted_levels, reference = ref,
    reference_requested = !is.null(reference),
    weight_kind = if (is.null(kind)) "" else kind, conf_level = conf, rows = rows,
    keys = keys,
    coefficients = mn_g17(beta), std_errors = mn_g17(se), p_values = mn_g17(2 * pnorm(-abs(z))),
    ci_lower = mn_g17(beta - q * se), ci_upper = mn_g17(beta + q * se),
    vcov = mn_g17(as.vector(V)),
    deviance = mn_g17(-2 * pp$ll), null_deviance = mn_g17(null_dev),
    n_obs = sum(w > 0), fitted_probabilities = mn_g17(as.vector(t(pp$P))))
  if (is.null(kind) || kind == "frequency") {
    doc$two_log_lik <- mn_g17(2 * pp$ll)
    doc$aic <- mn_g17(-2 * pp$ll + 2 * k * 3)
    mn_check(paste(name, "multinom AIC"), m$AIC, -2 * pp$ll + 2 * k * 3, 1e-8)
  }
  mn_cases[[length(mn_cases) + 1]] <<- doc
}

set.seed(20261102)
mn_draw <- function(n, B) {
  d <- data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(rnorm(n), 3))
  eta <- cbind(0, cbind(1, d$x1, d$x2) %*% B)
  P <- exp(eta) / rowSums(exp(eta))
  d$y <- apply(P, 1, function(p) sample.int(length(p), 1, prob = p)) - 1
  d
}

d <- mn_draw(150, cbind(c(0.3, 0.8, -0.5), c(-0.4, 1.1, 0.6)))
mn_case("three_levels", d, c("a", "b", "c"))
mn_case("three_levels_conf90", d, c("a", "b", "c"), conf = 0.90)

# Dictionary order is not alphabetical; the reference is named.
d <- mn_draw(200, cbind(c(0.2, -0.6, 0.4), c(0.5, 0.3, -0.8), c(-0.3, 0.9, 0.2)))
mn_case("four_levels_reference", d, c("low", "mid", "high", "top"), reference = "high")

d <- mn_draw(60, cbind(c(0.1, 0.7, -0.4), c(-0.2, -0.5, 0.9)))
mn_case("three_levels_freq", d, c("a", "b", "c"), weight = sample(1:4, 60, replace = TRUE), kind = "frequency")
mn_case("three_levels_prob", d, c("a", "b", "c"), weight = round(runif(60, 0.2, 3), 3), kind = "probability")

# A dictionary level no row carries is dropped (multinom's "group is empty").
d <- mn_draw(90, cbind(c(0.2, 0.6, 0.3), c(-0.1, -0.7, 0.5)))
d$y <- ifelse(d$y == 0, 0, d$y + 1)
mn_case("empty_level", d, c("a", "unused", "b", "c"))

write_golden("multinomial", list(
  generator = "scripts/reference/gen_multinomial.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
    nnet = as.character(packageVersion("nnet"))),
  note = paste(
    "multinom(y ~ x1 + x2) Newton-polished to the MLE. keys category-major: <level>:(intercept), <level>:x1, <level>:x2 per",
    "non-reference level in fitted order (categories: reference first, then the observed levels in dictionary order);",
    "vcov row-major over keys; fitted_probabilities row-major n x L over categories."),
  cases = mn_cases
))
