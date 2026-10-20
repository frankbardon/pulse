# gen_ols_penalty.R — R oracle for penalised REG_OLS (model-workbench
# E3-S1, #341): l2 / l1 / elasticnet under the ONE objective every
# REG_OLS penalty and REG_GLM share (glmnet, standardize = FALSE):
#
#   (1/2N*) * sum(w * (y - b0 - X b)^2) + alpha * [(1 - l1_ratio)/2 * ||b||^2 + l1_ratio * ||b||_1]
#
# predictors on their own units, intercept unpenalised, N* = sum(w).
#
# Writes <out_dir>/ols_penalty.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_ols_penalty.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer). CI never runs R.
#
# Pinned packages: glmnet 5.1, jsonlite.
#
# Ridge (l2) cases also pin the closed-form ridge covariance
# (ridge_inference below, reference-oracles E2-S2). The ridge_unpenalized_*
# cases pin `unpenalized` (custom-regressions E3-S1, FR-32): the listed
# predictors carry glmnet penalty.factor 0, so the objective's ridge term
# runs over the others only; lasso / elastic net
# carry none — selection makes their sampling distribution non-standard,
# so Pulse reports their standard errors and p-values as null.
#
# glmnet's gaussian family standardises y internally (by its weighted
# population SD s), which rescales its ridge term: on the raw y it does
# NOT minimise the objective above for l1_ratio < 1. The oracle
# therefore fits the unit-SD response y~ = y/s, on which the internal
# standardisation is the identity. Substituting b = s*b~ into the
# objective and dividing by s^2 gives an elastic net on y~ with
#   lambda' = alpha*(1 - r) + alpha*r/s,   alpha' = (alpha*r/s) / lambda'
# (r = l1_ratio), so glmnet(x, y~, alpha = alpha', lambda = lambda')
# times s is the objective's minimiser. Every case is then self-checked
# against the KKT conditions of the objective above, written out here,
# at relative 1e-10 — the fixture pins the stated objective, not a
# glmnet convention.
#
# The design is ols_lasso_test.go's lassoFixture (50 rows, 5 predictors,
# signal on x0 / x2) recomputed here; the Go test fits the rows stored
# in the golden, so the two never need to agree on sin().

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

op_num <- function(x) {
  if (length(x) != 1) stop("op_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
op_vec <- function(v) lapply(unname(as.numeric(v)), op_num)

op_cases <- list()

# ridge_inference is the closed-form ridge fit and its covariance in R
# matrix algebra, on Pulse's scaling (frequency weights w, N* = sum(w)):
# with the weighted centred design Xc and Gram G = Xc' W Xc,
#
#   A      = G + N* * alpha * I          (glmnet standardize = FALSE)
#   beta   = A^-1 Xc' W yc,              b0 = ybar - mu' beta
#   sigma2 = RSS / (N* - p - 1)
#   V      = sigma2 * A^-1 G A^-1        (the ridge sandwich)
#   Var(b0) = sigma2 / N* + mu' V mu
#
# and Student-t p-values on N* - p - 1 df. beta is cross-checked against
# glmnet's at relative 1e-10 before anything is written.
#
# With unpenalized predictors the penalty is alpha * diag(pf), pf_j = 0 for
# the listed ones (1 otherwise): A = G + N* * alpha * diag(pf).
ridge_inference <- function(X, y, w, alpha, pf = rep(1, ncol(X))) {
  nstar <- sum(w)
  mu <- colSums(w * X) / nstar
  ybar <- sum(w * y) / nstar
  Xc <- sweep(X, 2, mu)
  yc <- y - ybar
  G <- crossprod(Xc, w * Xc)
  Ainv <- solve(G + nstar * alpha * diag(pf, ncol(X)))
  beta <- drop(Ainv %*% crossprod(Xc, w * yc))
  b0 <- ybar - sum(mu * beta)
  rss <- sum(w * (yc - drop(Xc %*% beta))^2)
  df <- nstar - ncol(X) - 1
  sigma2 <- rss / df
  S <- Ainv %*% G %*% Ainv
  v0 <- sigma2 / nstar + sigma2 * drop(t(mu) %*% S %*% mu)
  se <- sqrt(c(v0, sigma2 * diag(S)))
  bb <- c(b0, beta)
  list(b = bb, se = se, p = 2 * pt(-abs(bb / se), df), df = df)
}

# unpenalized names predictors with glmnet penalty.factor 0 (ridge only in
# Pulse). glmnet rescales penalty.factor to sum to nvars, which scales the
# effective lambda by nvars / sum(pf); lambda' * sum(pf) / nvars undoes it,
# so the penalised predictors see exactly Pulse's alpha.
op_case <- function(name, X, y, w, penalty, alpha, l1_ratio = 0, unpenalized = character()) {
  r <- switch(penalty, l2 = 0, l1 = 1, elasticnet = l1_ratio)
  stopifnot(all(unpenalized %in% colnames(X)), length(unpenalized) == 0 || penalty == "l2")
  pf <- ifelse(colnames(X) %in% unpenalized, 0, 1)
  wn <- w / sum(w)
  s <- sqrt(sum(wn * (y - sum(wn * y))^2))
  lam <- alpha * (1 - r) + alpha * r / s
  mix <- (alpha * r / s) / lam
  fit <- if (length(unpenalized) == 0) {
    glmnet(X, y / s, family = "gaussian", weights = w, alpha = mix, lambda = lam,
           standardize = FALSE, intercept = TRUE,
           control = list(thresh = 1e-24, maxit = 1e8))
  } else {
    glmnet(X, y / s, family = "gaussian", weights = w, alpha = mix,
           lambda = lam * sum(pf) / ncol(X), penalty.factor = pf,
           standardize = FALSE, intercept = TRUE,
           control = list(thresh = 1e-24, maxit = 1e8))
  }
  b <- s * as.numeric(as.matrix(coef(fit)))
  Xd <- cbind(1, X)
  res <- y - as.numeric(Xd %*% b)
  score <- as.numeric(t(Xd) %*% (w * res)) / sum(w)
  scale <- max(1, max(abs(score)))
  if (abs(score[1]) > 1e-10 * scale) stop(name, ": intercept score ", score[1])
  for (j in seq_along(b)[-1]) {
    if (b[j] != 0) {
      k <- score[j] - alpha * (1 - r) * pf[j - 1] * b[j] - alpha * r * pf[j - 1] * sign(b[j])
      if (abs(k) > 1e-10 * scale) stop(name, ": KKT active ", j, " residual ", k)
    } else if (abs(score[j]) > alpha * r * pf[j - 1] * (1 + 1e-10) + 1e-14) {
      stop(name, ": KKT inactive ", j)
    }
  }
  doc <- list(
    name = name, penalty = penalty, alpha = op_num(alpha),
    l1_ratio = op_num(if (penalty == "elasticnet") l1_ratio else 0),
    predictors = I(colnames(X)), weighted = any(w != 1),
    rows = lapply(seq_len(nrow(X)), function(i) op_vec(c(X[i, ], y[i], w[i]))),
    coefficients = op_vec(b), zero = I(colnames(X)[b[-1] == 0]))
  if (length(unpenalized) > 0) doc$unpenalized <- I(unpenalized)
  if (penalty == "l2") {
    inf <- ridge_inference(X, y, w, alpha, pf)
    rel <- max(abs(inf$b - b) / pmax(abs(b), 1e-300))
    if (rel > 1e-10) stop(name, ": closed-form ridge beta differs from glmnet by ", rel)
    doc$std_errors <- op_vec(inf$se)
    doc$p_values <- op_vec(inf$p)
    doc$df <- op_num(inf$df)
  }
  op_cases[[length(op_cases) + 1]] <<- doc
}

i <- 0:49
X <- cbind(x0 = sin(0.13 * i), x1 = cos(0.27 * i), x2 = sin(0.41 * i + 0.5),
           x3 = cos(0.55 * i + 1.3), x4 = sin(0.71 * i + 2.1))
y <- 0.5 + 2 * X[, "x0"] - 3 * X[, "x2"] + 0.05 * sin(0.93 * i + 0.7)
ones <- rep(1, 50)
w <- 1 + (i %% 3)

op_case("lasso_0.05", X, y, ones, "l1", 0.05)
op_case("lasso_0.1", X, y, ones, "l1", 0.1)
op_case("lasso_0.2", X, y, ones, "l1", 0.2)
op_case("elasticnet_0.05_0.5", X, y, ones, "elasticnet", 0.05, 0.5)
op_case("elasticnet_0.1_0.5", X, y, ones, "elasticnet", 0.1, 0.5)
op_case("ridge_0.5", X, y, ones, "l2", 0.5)
op_case("elasticnet_weighted", X, y, w, "elasticnet", 0.1, 0.3)
op_case("lasso_weighted", X, y, w, "l1", 0.1)
op_case("ridge_weighted", X, y, w, "l2", 0.3)
op_case("ridge_0.05", X, y, ones, "l2", 0.05)
# Ridge with unpenalized predictors (glmnet penalty.factor 0). glmnet's
# coordinate descent runs to thresh 1e-24 / maxit 1e8, converged far past
# the 1e-10 the closed form (ridge_inference) is held to above, so the
# documented tolerance stays relative 1e-10.
op_case("ridge_unpenalized_x0", X, y, ones, "l2", 0.5, unpenalized = "x0")
op_case("ridge_unpenalized_weighted", X, y, w, "l2", 0.3, unpenalized = c("x0", "x2"))

op_doc <- list(
  generator = "scripts/reference/gen_ols_penalty.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(glmnet = as.character(packageVersion("glmnet")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_OLS penalty l2 / l1 / elasticnet: glmnet's standardize = FALSE objective, intercept unpenalised",
  r_function = "s * coef(glmnet(x, y / s, gaussian, weights, alpha = alpha', lambda = lambda', standardize = FALSE, control = list(thresh = 1e-24, maxit = 1e8)))",
  note = paste(
    "Pulse alpha / l1_ratio map to glmnet lambda' / alpha' on the unit-SD response y/s (s the weighted population SD): lambda' = alpha*(1 - r) + alpha*r/s, alpha' = (alpha*r/s)/lambda'.",
    "rows are [predictors..., y, w]; N* = sum(w).",
    "Every case is self-checked against the KKT conditions of the stated objective at relative 1e-10.",
    "l2 cases add std_errors / p_values / df: the closed-form ridge covariance sigma2 * A^-1 G A^-1 (A = G + N* alpha I, G the weighted centred Gram, sigma2 = RSS/(N* - p - 1)), Var(b0) = sigma2/N* + mu' V mu, Student-t p on N* - p - 1 df, computed by R matrix algebra (ridge_inference).",
    "l1 / elasticnet cases carry no inference: their standard errors and p-values are undefined (null in Pulse).",
    "A case listing unpenalized fits glmnet with penalty.factor 0 on those predictors and lambda' * sum(pf) / nvars (undoing glmnet's penalty.factor rescaling); its ridge covariance uses A = G + N* alpha diag(pf)."),
  cases = op_cases)
write_golden("ols_penalty", op_doc)
