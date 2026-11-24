# gen_glm_penalty.R — R oracle for penalised REG_GLM (model-workbench
# E3-S1, #341): l2 / l1 / elasticnet on the binomial and poisson
# families under glmnet's objective,
#
#   -loglik(b)/N* + alpha * [(1 - l1_ratio)/2 * ||b||^2 + l1_ratio * ||b||_1]
#
# unstandardised, the intercept unpenalised, N* = sum(w).
#
# Writes <out_dir>/glm_penalty.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_glm_penalty.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned packages: glmnet 5.1 (CRAN; install.packages("glmnet")),
# jsonlite. glmnet 5.x takes thresh / maxit through `control`; the call is
# glmnet(x, y, family, weights, alpha = l1_ratio, lambda = alpha,
# standardize = FALSE, intercept = TRUE, control = list(thresh = 1e-20,
# maxit = 1e7)). Pulse's `alpha` is glmnet's `lambda` and Pulse's
# `l1_ratio` glmnet's `alpha` (l2 = 0, l1 = 1). thresh is far below the
# 1e-14 the story named: at 1e-14 glmnet stops with KKT residuals ~1e-8
# on the weighted elastic-net case (its threshold is relative to the
# null deviance); 1e-20 brings every case to ~1e-13.
#
# Self-checks before anything is written: glmnet's solution is held to
# the KKT conditions of the objective ABOVE (written out here, not
# glmnet's) at relative 1e-10 — the intercept's score is zero, a nonzero
# coefficient's penalised score is zero, a zero coefficient's score lies
# inside +-N*.alpha.l1_ratio — so the fixture pins the stated objective,
# not a glmnet convention.
#
# std_errors is the penalised-Fisher sandwich H^-1 A H^-1 at glmnet's
# solution, A = X'diag(w.V(mu))X, H = A + N*.alpha.(1 - l1_ratio).D
# (D = diag(0, 1, ..., 1)), computed here by hand over every coefficient
# for l2. l1 / elasticnet carry none (every entry JSON null): the active
# set is chosen by the data, so a sandwich that treats it as fixed has no
# valid sampling distribution — Pulse reports those standard errors and
# p-values as null (reference-oracles E2-S2).
#
# The *_unpenalized* cases pin `unpenalized` (regression-depth-2 E1-S1,
# #487) on every penalty kind: the listed predictors carry glmnet
# penalty.factor pf = 0 (1 otherwise), so the penalty reads
# alpha * sum_j pf_j * [...]. glmnet rescales penalty.factor to sum to
# nvars, which scales its effective lambda by nvars / sum(pf); lambda =
# alpha * sum(pf) / nvars undoes it (as gen_ols_penalty.R). The KKT checks
# and the l2 sandwich read the same pf: H = A + N*.alpha.(1 - l1_ratio).
# diag(0, pf).

suppressPackageStartupMessages({
  library(jsonlite)
  library(glmnet)
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

gp_num <- function(x) {
  if (length(x) != 1) stop("gp_num() takes a scalar")
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
gp_vec <- function(v) lapply(unname(as.numeric(v)), gp_num)

gp_cases <- list()

# gp_case fits one penalised model on the design X (n x p, named
# columns), response y and weights w, self-checks it, and records it.
gp_case <- function(name, family, X, y, w, penalty, alpha, l1_ratio = 0, unpenalized = character()) {
  mix <- switch(penalty, l2 = 0, l1 = 1, elasticnet = l1_ratio)
  stopifnot(all(unpenalized %in% colnames(X)))
  pf <- ifelse(colnames(X) %in% unpenalized, 0, 1)
  fit <- if (length(unpenalized) == 0) {
    glmnet(X, y, family = family, weights = w, alpha = mix, lambda = alpha,
           standardize = FALSE, intercept = TRUE,
           control = list(thresh = 1e-20, maxit = 1e7))
  } else {
    glmnet(X, y, family = family, weights = w, alpha = mix,
           lambda = alpha * sum(pf) / ncol(X), penalty.factor = pf,
           standardize = FALSE, intercept = TRUE,
           control = list(thresh = 1e-20, maxit = 1e7))
  }
  b <- as.numeric(as.matrix(coef(fit)))
  Xd <- cbind(1, X)
  eta <- as.numeric(Xd %*% b)
  mu <- switch(family, binomial = 1 / (1 + exp(-eta)), poisson = exp(eta))
  v <- switch(family, binomial = mu * (1 - mu), poisson = mu)
  nstar <- sum(w)
  # KKT of -sum(w l)/N* + alpha * P: score_j = sum(w (y - mu) x_j) / N*.
  score <- as.numeric(t(Xd) %*% (w * (y - mu))) / nstar
  l1 <- alpha * mix
  l2 <- alpha * (1 - mix)
  scale <- max(1, max(abs(score)))
  if (abs(score[1]) > 1e-10 * scale) stop(name, ": intercept score ", score[1])
  for (j in seq_along(b)[-1]) {
    f <- pf[j - 1]
    if (b[j] != 0) {
      r <- score[j] - l2 * f * b[j] - l1 * f * sign(b[j])
      if (abs(r) > 1e-10 * scale) stop(name, ": KKT active ", j, " residual ", r)
    } else if (abs(score[j]) > l1 * f * (1 + 1e-10) + 1e-14) {
      stop(name, ": KKT inactive ", j, " |score| ", abs(score[j]), " > ", l1 * f)
    }
  }
  # Penalised-Fisher sandwich (l2 only; l1 / elasticnet: no inference).
  se <- rep(NA_real_, length(b))
  if (penalty == "l2") {
    A <- t(Xd) %*% (Xd * (w * v))
    H <- A + diag(c(0, nstar * l2 * pf), length(b))
    Hi <- solve(H)
    se <- sqrt(diag(Hi %*% A %*% Hi))
  }
  dev <- 2 * sum(w * switch(family,
    binomial = ifelse(y == 1, -log(mu), -log(1 - mu)),
    poisson = ifelse(y > 0, y * log(y / mu), 0) - (y - mu)))
  rows <- lapply(seq_len(nrow(X)), function(i) gp_vec(c(X[i, ], y[i], w[i])))
  doc <- list(
    name = name, family = family, penalty = penalty,
    alpha = gp_num(alpha), l1_ratio = gp_num(if (penalty == "elasticnet") l1_ratio else 0),
    predictors = I(colnames(X)), weighted = any(w != 1),
    columns = I(c(colnames(X), "y", "w")), rows = rows,
    coefficients = gp_vec(b), std_errors = gp_vec(se),
    zero = I(colnames(X)[b[-1] == 0]),
    deviance = gp_num(dev))
  if (length(unpenalized) > 0) doc$unpenalized <- I(unpenalized)
  gp_cases[[length(gp_cases) + 1]] <<- doc
}

set.seed(20261018)
n <- 240
x1 <- round(rnorm(n), 4)
x2 <- round(rnorm(n, 5, 2), 4)
x3 <- round(runif(n, 0, 10), 4)
eta_b <- -1 + 0.9 * x1 - 0.35 * (x2 - 5)
yb <- as.numeric(runif(n) < 1 / (1 + exp(-eta_b)))
Xb <- cbind(x1 = x1, x2 = x2, x3 = x3)
ones <- rep(1, n)
wb <- rep(c(1, 2, 3, 1, 2), length.out = n)

gp_case("binomial_l2", "binomial", Xb, yb, ones, "l2", 0.05)
gp_case("binomial_l1", "binomial", Xb, yb, ones, "l1", 0.1)
gp_case("binomial_elasticnet", "binomial", Xb, yb, ones, "elasticnet", 0.2, 0.5)
gp_case("binomial_l2_weighted", "binomial", Xb, yb, wb, "l2", 0.05)
gp_case("binomial_elasticnet_weighted", "binomial", Xb, yb, wb, "elasticnet", 0.03, 0.5)

np <- 180
z1 <- round(rnorm(np), 4)
z2 <- round(runif(np, 0, 4), 4)
z3 <- round(rnorm(np, 0, 3), 4)
yp <- rpois(np, exp(0.4 + 0.5 * z1 + 0.25 * z2))
Xp <- cbind(z1 = z1, z2 = z2, z3 = z3)
onesp <- rep(1, np)

gp_case("poisson_l2", "poisson", Xp, yp, onesp, "l2", 0.05)
gp_case("poisson_l1", "poisson", Xp, yp, onesp, "l1", 0.15)
gp_case("poisson_elasticnet", "poisson", Xp, yp, onesp, "elasticnet", 0.04, 0.3)

# Complete separation (gen_glm_irls.R's complete_separation, plus a
# second, non-separating predictor: glmnet needs two columns): the MLE
# is at infinity, the l2 estimate finite.
xs <- seq(-19.5, 19.5, by = 1)
zs <- (seq_along(xs) %% 3) - 1
gp_case("separated_l2", "binomial", cbind(x = xs, z = zs), as.numeric(xs >= 0), rep(1, length(xs)), "l2", 0.1)

# unpenalized (regression-depth-2 E1-S1, #487): every penalty kind, both
# families, weighted too. x3 / z3 carry no signal, so the plain l1 fits
# zero them; penalty factor 0 keeps them in the active set, unshrunk.
gp_case("binomial_l2_unpenalized", "binomial", Xb, yb, ones, "l2", 0.05, unpenalized = "x1")
gp_case("binomial_l1_unpenalized", "binomial", Xb, yb, ones, "l1", 0.1, unpenalized = "x3")
gp_case("binomial_elasticnet_unpenalized_weighted", "binomial", Xb, yb, wb, "elasticnet", 0.03, 0.5, unpenalized = c("x1", "x3"))
gp_case("poisson_l1_unpenalized", "poisson", Xp, yp, onesp, "l1", 0.15, unpenalized = "z3")
gp_case("poisson_l2_unpenalized", "poisson", Xp, yp, onesp, "l2", 0.05, unpenalized = c("z1", "z2"))

gp_doc <- list(
  generator = "scripts/reference/gen_glm_penalty.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(glmnet = as.character(packageVersion("glmnet")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_GLM penalty l2 / l1 / elasticnet: glmnet's objective, unstandardised, intercept unpenalised",
  r_function = "glmnet(x, y, family, weights, alpha = l1_ratio, lambda = alpha, standardize = FALSE, control = list(thresh = 1e-20, maxit = 1e7))",
  note = paste(
    "Pulse alpha = glmnet lambda; Pulse l1_ratio = glmnet alpha (l2 0, l1 1).",
    "rows are [predictors..., y, w]; N* = sum(w).",
    "std_errors: l2 the penalised-Fisher sandwich H^-1 A H^-1 at glmnet's solution; l1 / elasticnet null on every coefficient (no valid inference after data-driven selection).",
    "A case listing unpenalized fits glmnet with penalty.factor 0 on those predictors and lambda = alpha * sum(pf) / nvars (undoing glmnet's penalty.factor rescaling); its KKT checks and l2 sandwich read H = A + N*.alpha.(1 - l1_ratio).diag(0, pf).",
    "deviance: the unpenalised deviance at the penalised estimate.",
    "Every case is self-checked against the KKT conditions of the stated objective at relative 1e-10."),
  cases = gp_cases)
write_golden("glm_penalty", gp_doc)
