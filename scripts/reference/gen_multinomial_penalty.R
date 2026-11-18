# gen_multinomial_penalty.R — R oracle for penalised REG_MULTINOMIAL (U68
# E2-S3, #658, decision D6): ridge / lasso / elastic net in glmnet's
# symmetric K-category form, per-coefficient ("ungrouped") penalty,
#
#   -loglik(B)/N* + alpha * sum_j pf_j [(1 - l1_ratio)/2 * sum_k b_kj^2 + l1_ratio * sum_k |b_kj|]
#
# every category k carrying its own coefficients b_k (no reference), the
# intercepts unpenalised, predictors on their own units (standardize =
# FALSE), N* = sum(w), pf_j = 0 for an `unpenalized` predictor and 1
# otherwise. The objective is invariant to adding one constant to every
# category's coefficient on an unpenalised term (the intercept, an
# `unpenalized` predictor), so glmnet centres those to mean zero over the
# categories; a penalised term's coefficients are pinned by the penalty
# (and for pure lasso by glmnet's median centring, which leaves odd K
# unique — every lasso case here has K = 3).
#
# Writes <out_dir>/multinomial_penalty.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_multinomial_penalty.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned packages: glmnet 5.1, jsonlite. The call is glmnet(x, y, family
# = "multinomial", type.multinomial = "ungrouped", weights = w, alpha =
# l1_ratio, lambda = alpha * sum(pf) / nvars, penalty.factor = pf,
# standardize = FALSE, control = list(thresh = 1e-20, maxit = 1e7)):
# Pulse's `alpha` is glmnet's `lambda`, Pulse's `l1_ratio` glmnet's
# `alpha` (l2 = 0, l1 = 1), and lambda * sum(pf) / nvars undoes glmnet's
# rescaling of penalty.factor to sum to nvars (as gen_ols_penalty.R).
#
# Polish: glmnet's solution is taken to the objective's exact minimiser
# by Newton on the active set with its signs fixed (the l1 term linear
# there), the flat direction of every unpenalised term removed by a
# rank-one gauge term (the gradient is orthogonal to it), until the step
# is below 1e-15 relative; the unpenalised terms are then centred.
# Asserted before anything is written: the polished coefficients agree
# with glmnet's at 1e-6 and satisfy the KKT conditions of the objective
# ABOVE (written out here, not glmnet's) at relative 1e-10 — the active
# coefficients' penalised score is zero, a zero coefficient's score lies
# inside +-alpha.l1_ratio.pf_j, every unpenalised term sums to zero over
# the categories — so the fixture pins the stated objective, not a
# glmnet convention.
#
# Recorded per case: coefficients category-major over `levels` (each
# category's (intercept), x1, x2, x3 — Pulse's key order), the zero
# coefficients' keys, the unpenalised deviance -2 loglik and the null
# deviance at the polished estimate, and a grid of new [x1, x2, x3] rows
# with their category probabilities (asserted equal to predict(glmnet,
# type = "response") at 1e-6) and every category's linear predictor
# x' b_k (glmnet's type = "link"). Standard errors are not recorded:
# Pulse reports none for a penalised multinomial fit.
#
# Seeded (set.seed(20261117)). Rows are [x1, x2, x3, y, w], y the
# 0-based index of the row's level in `levels` (the target's dictionary
# order).

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

mp_num <- function(x) {
  if (length(x) != 1) stop("mp_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
mp_vec <- function(v) lapply(unname(as.numeric(v)), mp_num)

# mp_probs is every row's category probabilities (n x K) for the
# coefficient matrix B (q x K, intercept first) on the design Xd (n x q).
mp_probs <- function(B, Xd) {
  E <- Xd %*% B
  E <- E - apply(E, 1, max)
  P <- exp(E)
  P / rowSums(P)
}

# mp_parts returns the smooth part's value -loglik/N*, its gradient
# (q x K) and Hessian (qK x qK, category-major) at B.
mp_parts <- function(B, Xd, Y, w) {
  nstar <- sum(w)
  P <- mp_probs(B, Xd)
  ll <- sum(w * log(rowSums(P * Y)))
  G <- -t(Xd) %*% (w * (Y - P)) / nstar
  q <- ncol(Xd); K <- ncol(Y)
  H <- matrix(0, q * K, q * K)
  for (k in seq_len(K)) for (l in seq_len(K)) {
    c <- w * P[, k] * ((k == l) - P[, l])
    H[(k - 1) * q + seq_len(q), (l - 1) * q + seq_len(q)] <- t(Xd) %*% (Xd * c) / nstar
  }
  list(value = -ll / nstar, grad = G, hess = H, ll = ll)
}

mp_cases <- list()

mp_case <- function(name, d, levels, penalty, alpha, l1_ratio = 0, unpenalized = character(), weighted = FALSE) {
  preds <- c("x1", "x2", "x3")
  X <- as.matrix(d[, preds])
  y <- factor(levels[d$y + 1], levels = levels)
  w <- if (weighted) d$w else rep(1, nrow(d))
  mix <- switch(penalty, l2 = 0, l1 = 1, elasticnet = l1_ratio)
  pf <- ifelse(preds %in% unpenalized, 0, 1)
  fit <- glmnet(X, y, family = "multinomial", type.multinomial = "ungrouped", weights = w,
                alpha = mix, lambda = alpha * sum(pf) / length(pf), penalty.factor = pf,
                standardize = FALSE, intercept = TRUE,
                control = list(thresh = 1e-20, maxit = 1e7))
  K <- length(levels)
  q <- length(preds) + 1
  B0 <- sapply(coef(fit)[levels], function(m) as.numeric(as.matrix(m)))
  Xd <- cbind(1, X)
  Y <- diag(K)[as.integer(y), ]
  l1 <- alpha * mix
  l2 <- alpha * (1 - mix)
  pen <- c(0, pf)                      # per term: intercept unpenalised
  flat <- which(pen == 0)              # terms the objective is flat along
  B <- B0
  for (iter in 1:200) {
    parts <- mp_parts(B, Xd, Y, w)
    b <- as.vector(B)
    penv <- rep(pen, K)
    act <- which(b != 0 | penv == 0)
    g <- as.vector(parts$grad) + penv * (l2 * b + l1 * sign(b))
    H <- parts$hess + diag(rep(pen * l2, K))
    for (j in flat) {
      u <- rep(0, q * K)
      u[(seq_len(K) - 1) * q + j] <- 1 / sqrt(K)
      H <- H + outer(u, u)
    }
    step <- solve(H[act, act], g[act])
    nb <- b
    nb[act] <- b[act] - step
    B <- matrix(nb, q, K)
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(nb)))) break
  }
  for (j in flat) B[j, ] <- B[j, ] - mean(B[j, ])
  if (max(abs(B - B0)) > 1e-6 * max(1, max(abs(B)))) stop(name, ": polish moved glmnet's fit by ", max(abs(B - B0)))
  # KKT of the stated objective.
  parts <- mp_parts(B, Xd, Y, w)
  G <- parts$grad
  scale <- max(1, max(abs(G)))
  for (k in seq_len(K)) for (j in seq_len(q)) {
    bj <- B[j, k]
    if (pen[j] == 0) {
      if (abs(G[j, k]) > 1e-10 * scale) stop(name, ": unpenalised score ", G[j, k])
    } else if (bj != 0) {
      r <- G[j, k] + l2 * bj + l1 * sign(bj)
      if (abs(r) > 1e-10 * scale) stop(name, ": KKT active ", j, ",", k, " residual ", r)
    } else if (abs(G[j, k]) > l1 * (1 + 1e-10) + 1e-14) {
      stop(name, ": KKT inactive ", j, ",", k, " |score| ", abs(G[j, k]), " > ", l1)
    }
  }
  for (j in flat) if (abs(sum(B[j, ])) > 1e-12 * max(1, max(abs(B[j, ])))) stop(name, ": term ", j, " not centred")
  terms <- c("(intercept)", preds)
  keys <- as.vector(outer(terms, levels, function(t, l) paste(l, t, sep = ":")))
  shares <- colSums(w * Y) / sum(w)
  null_dev <- -2 * sum(w * log(shares[as.integer(y)]))
  grid <- expand.grid(x1 = c(-1.5, 0, 1.2), x2 = c(-0.8, 0.9), x3 = c(0.5, 2.5))
  Gd <- cbind(1, as.matrix(grid))
  Pg <- mp_probs(B, Gd)
  Pr <- predict(fit, newx = as.matrix(grid), s = fit$lambda, type = "response")[, levels, 1]
  if (max(abs(Pg - Pr)) > 1e-6) stop(name, ": grid probabilities differ from predict(glmnet) by ", max(abs(Pg - Pr)))
  mp_cases[[length(mp_cases) + 1]] <<- list(
    name = name, levels = I(levels), penalty = penalty,
    alpha = mp_num(alpha), l1_ratio = mp_num(if (penalty == "elasticnet") l1_ratio else 0),
    unpenalized = I(unpenalized), weighted = weighted,
    rows = lapply(seq_len(nrow(d)), function(i) mp_vec(c(X[i, ], d$y[i], w[i]))),
    keys = I(keys), coefficients = mp_vec(as.vector(B)),
    zero = I(keys[as.vector(B) == 0]),
    deviance = mp_num(-2 * parts$ll), null_deviance = mp_num(null_dev),
    score_rows = lapply(seq_len(nrow(grid)), function(i) mp_vec(unlist(grid[i, ]))),
    score_probabilities = mp_vec(as.vector(t(Pg))),
    score_link = mp_vec(as.vector(t(Gd %*% B))))
}

set.seed(20261117)
mp_draw <- function(n, E) {
  x1 <- round(rnorm(n), 4)
  x2 <- round(rnorm(n, 0.5, 1.5), 4)
  x3 <- round(runif(n, 0, 3), 4)
  eta <- E(x1, x2, x3)
  P <- exp(eta); P <- P / rowSums(P)
  y <- apply(P, 1, function(p) sample(seq_along(p) - 1, 1, prob = p))
  data.frame(x1 = x1, x2 = x2, x3 = x3, y = y, w = sample(1:4, n, replace = TRUE))
}

d3 <- mp_draw(180, function(x1, x2, x3) cbind(0, 0.4 + 1.1 * x1 - 0.3 * x2, -0.2 - 0.7 * x1 + 0.6 * x3))
mp_case("three_l2", d3, c("a", "b", "c"), "l2", 0.05)
mp_case("three_l1", d3, c("a", "b", "c"), "l1", 0.02)
mp_case("three_elasticnet", d3, c("a", "b", "c"), "elasticnet", 0.03, 0.4)
mp_case("three_l2_unpenalized", d3, c("a", "b", "c"), "l2", 0.08, unpenalized = "x1")
mp_case("three_l1_unpenalized", d3, c("a", "b", "c"), "l1", 0.03, unpenalized = c("x1", "x3"))
mp_case("three_elasticnet_unpenalized", d3, c("a", "b", "c"), "elasticnet", 0.04, 0.6, unpenalized = "x2")
mp_case("three_elasticnet_weighted", d3, c("a", "b", "c"), "elasticnet", 0.02, 0.5, weighted = TRUE)

d4 <- mp_draw(240, function(x1, x2, x3) cbind(0, 0.9 * x1, -0.8 * x1 + 0.5 * x2, 0.3 + 0.4 * x3 - 0.5 * x2))
mp_case("four_l2", d4, c("low", "mid", "high", "top"), "l2", 0.04)
mp_case("four_elasticnet", d4, c("low", "mid", "high", "top"), "elasticnet", 0.025, 0.3)

write_golden("multinomial_penalty", list(
  generator = "scripts/reference/gen_multinomial_penalty.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(glmnet = as.character(packageVersion("glmnet")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_MULTINOMIAL penalty l2 / l1 / elasticnet: glmnet's symmetric multinomial, ungrouped, unstandardised, intercepts unpenalised",
  r_function = "glmnet(x, y, family = \"multinomial\", type.multinomial = \"ungrouped\", weights, alpha = l1_ratio, lambda = alpha * sum(pf) / nvars, penalty.factor = pf, standardize = FALSE, control = list(thresh = 1e-20, maxit = 1e7))",
  note = paste(
    "Pulse alpha = glmnet lambda; Pulse l1_ratio = glmnet alpha (l2 0, l1 1); pf 0 on `unpenalized`, lambda * sum(pf) / nvars undoes glmnet's rescaling.",
    "rows are [x1, x2, x3, y, w], y 0-based over levels; N* = sum(w).",
    "coefficients: category-major over levels, terms (intercept), x1, x2, x3; Newton-polished on the active set to the stated objective, unpenalised terms centred; KKT self-checked at relative 1e-10.",
    "deviance: the unpenalised -2 loglik at the penalised estimate; null_deviance at the category shares.",
    "score_probabilities / score_link: the grid rows' category probabilities and linear predictors x'b_k, row-major."),
  cases = mp_cases))
