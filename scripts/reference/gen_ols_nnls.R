# gen_ols_nnls.R — R oracle for REG_OLS bounds.nonnegative
# (custom-regressions E3-S2, FR-31): least squares with the named slopes
# constrained >= 0, the intercept and the other slopes free,
#
#   minimise  sum(w * (y - b0 - X b)^2)   subject to  b_j >= 0, j in N
#
# on frequency weights (N* = sum(w), the rep() expansion of the rows).
#
# Writes <out_dir>/ols_nnls.json. Sourced from gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_ols_nnls.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer). CI never runs R.
#
# Pinned packages: nnls 1.6 (Lawson-Hanson, Fortran), jsonlite. nnls
# solves min ||A x - b|| over x >= 0 for EVERY column, so the oracle maps
# the problem onto it: the intercept is profiled out by centring X and y
# on their weighted means (b0 = ybar - xbar' b), the rows are scaled by
# sqrt(w), and each free slope enters as two non-negative columns (x and
# -x; b = x+ - x-). nnls on Homebrew R needs the Fortran workaround of
# docs/src/internals/regenerating-goldens.md (The R toolchain).
#
# Self-checks, every case, at relative 1e-10 before anything is written:
#   * the KKT conditions of the objective: the gradient
#     g = Xc' W (yc - Xc b) is 0 on every free or positive slope and
#     <= 0 on every slope held at 0;
#   * the coefficients equal lm.fit (Householder QR) on the final passive
#     set — the bound slopes dropped, the rest unconstrained — which is
#     what an active-set solution is;
#   * a no-binding case equals lm() on every predictor (the bounded fit
#     is plain OLS there).
#
# Emitted figures: coefficients (intercept first), active (the bounded
# predictors held at 0), r2, adj_r2 and residual_std_err on N* - p - 1
# df. Rows are drawn with a fixed seed and stored at %.17g; the Go test
# fits the stored rows.

suppressPackageStartupMessages({
  library(jsonlite)
  library(nnls)
})

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

nn_num <- function(x) {
  if (length(x) != 1) stop("nn_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
nn_vec <- function(v) lapply(unname(as.numeric(v)), nn_num)
nn_near <- function(a, b, tol, what, abs_floor = 0) {
  a <- as.numeric(a); b <- as.numeric(b)
  scale <- pmax(abs(a), abs(b), abs_floor)
  ok <- abs(a - b) <= tol * scale | scale == 0
  if (!all(ok)) stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / scale))
}

nn_cases <- list()

# nn_case: X (n x p, named columns), y, the bounded names, integer
# frequency weights w (all 1 = unweighted).
nn_case <- function(name, X, y, nonneg, w = rep(1, length(y)), expect_binding) {
  stopifnot(all(w == round(w)), all(w >= 1), all(nonneg %in% colnames(X)))
  p <- ncol(X)
  bounded <- colnames(X) %in% nonneg
  sw <- sum(w)
  xbar <- colSums(X * w) / sw
  ybar <- sum(y * w) / sw
  Xc <- sweep(X, 2, xbar)
  yc <- y - ybar
  rw <- sqrt(w)

  free <- which(!bounded)
  A <- cbind(Xc[, bounded, drop = FALSE], Xc[, free, drop = FALSE], -Xc[, free, drop = FALSE]) * rw
  fit <- nnls(A, yc * rw)
  sol <- fit$x
  nb <- sum(bounded)
  nf <- length(free)
  b <- numeric(p)
  b[bounded] <- sol[seq_len(nb)]
  b[free] <- sol[nb + seq_len(nf)] - sol[nb + nf + seq_len(nf)]

  # KKT of the stated objective.
  r <- yc - as.numeric(Xc %*% b)
  g <- as.numeric(crossprod(Xc, r * w))
  gscale <- max(abs(crossprod(Xc, yc * w)))
  held <- bounded & b == 0
  if (any(abs(g[!held]) > 1e-10 * gscale)) stop("self-check failed: ", name, " KKT stationarity")
  if (any(g[held] > 1e-10 * gscale)) stop("self-check failed: ", name, " KKT sign on a held slope")
  if (any(b[bounded] < 0)) stop("self-check failed: ", name, " a bounded slope is negative")

  # The active-set solution is OLS on the passive set.
  pass <- which(!held)
  lf <- lm.fit(cbind(1, X[, pass, drop = FALSE]) * rw, y * rw)
  bl <- numeric(p)
  bl[pass] <- lf$coefficients[-1]
  nn_near(b, bl, 1e-10, paste(name, "slopes vs lm.fit on the passive set"), abs_floor = max(abs(bl)))
  b <- bl
  b0 <- ybar - sum(xbar * b)
  nn_near(b0, lf$coefficients[1], 1e-10, paste(name, "intercept vs lm.fit"), abs_floor = abs(ybar))

  binding <- any(held)
  if (binding != expect_binding) stop("self-check failed: ", name, " binding = ", binding)
  if (!binding) {
    lmf <- lm(y ~ X, weights = w)
    nn_near(c(b0, b), coef(lmf), 1e-10, paste(name, "no-binding fit vs lm()"))
  }

  res <- y - b0 - as.numeric(X %*% b)
  rss <- sum(w * res^2)
  tss <- sum(w * yc^2)
  df <- sw - p - 1
  r2 <- 1 - rss / tss
  adj <- 1 - (1 - r2) * (sw - 1) / df

  rows <- lapply(seq_along(y), function(i) nn_vec(c(X[i, ], y[i], w[i])))
  nn_cases[[length(nn_cases) + 1]] <<- list(
    name = name,
    predictors = as.list(colnames(X)),
    nonnegative = as.list(nonneg),
    weighted = any(w != 1),
    rows = rows,
    coefficients = nn_vec(c(b0, b)),
    active = as.list(colnames(X)[held]),
    r2 = nn_num(r2),
    adj_r2 = nn_num(adj),
    residual_std_err = nn_num(sqrt(rss / df))
  )
}

set.seed(20261020)
n <- 60
x1 <- round(rnorm(n, 5, 2), 4)
x2 <- round(rnorm(n, 0, 1.5), 4)
x3 <- round(runif(n, -2, 3), 4)
noise <- round(rnorm(n, 0, 0.8), 4)

# No binding: every true slope positive, the unconstrained fit already
# non-negative — the bounded fit is plain OLS.
X <- cbind(x1 = x1, x2 = x2, x3 = x3)
y <- round(1.5 + 0.8 * x1 + 0.6 * x2 + 0.4 * x3 + noise, 4)
nn_case("no_binding", X, y, c("x1", "x2", "x3"), expect_binding = FALSE)

# Binding: x2's true slope is negative; bounded, it is held at 0.
y <- round(1.5 + 0.8 * x1 - 0.9 * x2 + 0.4 * x3 + noise, 4)
nn_case("binding_one", X, y, c("x1", "x2", "x3"), expect_binding = TRUE)

# Mixed: x2 bounded and binding, x3 free with a negative slope it keeps.
y <- round(1.5 + 0.8 * x1 - 0.9 * x2 - 0.7 * x3 + noise, 4)
nn_case("binding_mixed_free", X, y, c("x1", "x2"), expect_binding = TRUE)

# Two bind: x2 and x3 both negative, both bounded.
nn_case("binding_two", X, y, c("x2", "x3"), expect_binding = TRUE)

# Near-collinear: x4 is x1 plus a little noise (corr ~ 0.999); the
# signal loads on x1 and against x4, so the unconstrained x4 slope is
# negative and the bound holds it at 0.
x4 <- round(x1 + rnorm(n, 0, 0.1), 4)
Xc4 <- cbind(x1 = x1, x4 = x4, x3 = x3)
y <- round(2 + 1.2 * x1 - 0.5 * x4 + 0.3 * x3 + noise, 4)
nn_case("near_collinear", Xc4, y, c("x1", "x4"), expect_binding = TRUE)

# Frequency-weighted binding: the rep() expansion.
w <- (seq_len(n) %% 3) + 1
y <- round(1.5 + 0.8 * x1 - 0.9 * x2 + 0.4 * x3 + noise, 4)
nn_case("weighted_binding", X, y, c("x1", "x2", "x3"), w = w, expect_binding = TRUE)

write_golden("ols_nnls", list(
  generator = "scripts/reference/gen_ols_nnls.R",
  r_version = R.version.string,
  packages = list(
    nnls = as.character(packageVersion("nnls")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "REG_OLS bounds.nonnegative (least squares, named slopes >= 0)",
  r_function = "nnls::nnls on the centred, sqrt(w)-scaled design (free slopes split x / -x); lm.fit on the passive set",
  note = "coefficients intercept first; active = bounded predictors held at 0; r2 / adj_r2 / residual_std_err on N* - p - 1 df",
  cases = nn_cases
))
