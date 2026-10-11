# gen_robust_vcov.R — R oracle for REG_OLS robust coefficient covariance
# (RegressionSpec.Covariance: hc0-hc3, cluster CR1, hac Newey-West;
# U25 E5-S1).
#
# Sourced by gen_multivariate.R (`make reference`) after gen_distance.R:
# it reuses out_dir, num(), numv(), numm(), strs(), near(), expand(),
# n_star(), write_golden() and mv_meta(). Writes
# <out_dir>/mv_robust_vcov.json. CI never runs R: everything here is
# committed. A rerun on the pinned toolchain is byte-identical.
#
# Pinned packages (on top of gen_multivariate.R's): sandwich 3.1-3,
# lmtest 0.9-40. Install into the user library:
#   Rscript -e 'install.packages(c("sandwich", "lmtest"), lib = Sys.getenv("R_LIBS_USER"),
#     repos = "https://cloud.r-project.org")'
#
# The rows are seeded and written into the golden itself (no shared
# fixture moves): one weekly series of n = 48 rows in SHUFFLED row order
# (so order_by must sort), eight regions of six rows each (the
# clusters), a region random effect, AR(1) errors whose spread grows
# with x2 (heteroskedastic and serially dependent), and two weight
# columns. The Go test builds a typed cohort from them (region
# categorical, region_id an integer id of the same clusters, week an
# integer order).
#
# Estimators (X with intercept, k = ncol(X), B = (X'W*X)^-1, e the WLS
# residuals, h = rowSums((X %*% B) * X)), on w* = w * N*/sum(w) (N* =
# sum(w) frequency, Kish n_eff probability; w* = 1 unweighted):
#   hc0..hc3  B (sum_i omega_i w*_i e_i^2 x_i x_i') B, omega = 1, 1,
#             1/(1-h), 1/(1-h)^2; hc1 times N*/(N*-k).
#   cluster   B (sum_g s_g s_g') B * G/(G-1) * (N*-1)/(N*-k),
#             s_g = sum_{i in g} w*_i e_i x_i; inference on G-1 df.
#   hac       rows sorted by week, u_t = w*_t e_t x_t, Bartlett weights
#             1 - l/(L+1): B (G0 + sum_l b_l (G_l + G_l')) B * N*/(N*-k),
#             G_l = sum_t u_t u_{t-l}'.
# Asserted: unweighted == sandwich::vcovHC(type) / vcovCL(type = "HC1")
# / NeweyWest(prewhite = FALSE, adjust = TRUE), and p-values ==
# lmtest::coeftest(fit, vcov, df); frequency hc / cluster == the same
# sandwich calls on the rep()-expanded lm; weighted hac ==
# NeweyWest(lm(weights = w*), prewhite = FALSE, adjust = FALSE) *
# N*/(N*-k) (a period's copies share one residual, so their scores add).
# Probability hc / cluster are the frequency formula on w* (no expansion
# exists for fractional weights).

pinned_rv <- c(sandwich = "3.1.3", lmtest = "0.9.40")
for (p in names(pinned_rv)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != pinned_rv[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, pinned_rv[[p]]))
  }
}
suppressPackageStartupMessages({
  library(sandwich)
  library(lmtest)
})

rv_meta <- function(family, r_fn, note) {
  m <- mv_meta(family, r_fn, note)
  m$generator <- "scripts/reference/gen_robust_vcov.R (via gen_reference.R)"
  m$packages <- c(m$packages, lapply(as.list(pinned_rv), function(v) v))
  m
}

set.seed(905)
n_rv <- 48
regions <- paste0("region_", LETTERS[1:8])
rid <- sample(rep(1:8, each = 6))
x1 <- round(rnorm(n_rv), 3)
x2 <- round(runif(n_rv, 0, 3), 3)
x3 <- round(0.5 * x1 + rnorm(n_rv), 3)
eff <- rnorm(8, 0, 0.8)[rid]
eps <- numeric(n_rv)
eps[1] <- rnorm(1, 0, 0.5)
for (t in 2:n_rv) eps[t] <- 0.6 * eps[t - 1] + rnorm(1, 0, 0.3 + 0.4 * x2[t])
y <- round(1 + 2 * x1 - 0.7 * x2 + 0.3 * x3 + eff + eps, 3)
w_freq <- sample(1:4, n_rv, replace = TRUE)
w_prob <- round(runif(n_rv, 0.2, 3), 3)
rv <- data.frame(week = 1:n_rv, region = regions[rid], region_id = rid,
                 x1 = x1, x2 = x2, x3 = x3, y = y, w_freq = w_freq, w_prob = w_prob,
                 stringsAsFactors = FALSE)
rv <- rv[sample(n_rv), ]
rownames(rv) <- NULL

fml_rv <- y ~ x1 + x2 + x3
auto_lags <- function(n) floor(4 * (n / 100)^(2 / 9))

rv_cases <- list()
rv_case <- function(kind, type, lags = NULL) {
  d <- rv
  w <- switch(kind, none = rep(1, nrow(d)), frequency = d$w_freq, probability = d$w_prob)
  ns <- n_star(w, if (kind == "none") "frequency" else kind)
  ws <- w * ns / sum(w)
  X <- model.matrix(fml_rv, d); yv <- d$y; k <- ncol(X)
  B <- solve(crossprod(X * ws, X))
  b <- drop(B %*% crossprod(X * ws, yv))
  e <- drop(yv - X %*% b)
  h <- rowSums((X %*% B) * X)
  df <- ns - k
  G <- NA; L <- NA
  if (type %in% c("hc0", "hc1", "hc2", "hc3")) {
    om <- switch(type, hc0 = 1, hc1 = 1, hc2 = 1 / (1 - h), hc3 = 1 / (1 - h)^2)
    M <- crossprod(X * (om * ws * e^2), X)
    V <- B %*% M %*% B
    if (type == "hc1") V <- V * ns / (ns - k)
  } else if (type == "cluster") {
    S <- rowsum(X * (ws * e), d$region)
    G <- nrow(S)
    V <- B %*% crossprod(S) %*% B * G / (G - 1) * (ns - 1) / (ns - k)
    df <- G - 1
  } else {
    o <- order(d$week)
    U <- (X * (ws * e))[o, , drop = FALSE]
    L <- if (is.null(lags)) auto_lags(nrow(d)) else lags
    M <- crossprod(U)
    for (l in seq_len(L)) {
      if (l >= nrow(U)) break
      bw <- 1 - l / (L + 1)
      Gl <- crossprod(U[(l + 1):nrow(U), , drop = FALSE], U[1:(nrow(U) - l), , drop = FALSE])
      M <- M + bw * (Gl + t(Gl))
    }
    V <- B %*% M %*% B * ns / (ns - k)
  }
  dimnames(V) <- list(colnames(X), colnames(X)); names(b) <- colnames(X)
  se <- sqrt(diag(V))
  pv <- 2 * pt(-abs(b / se), df)
  q <- qt(0.975, df)

  # --- self-checks against sandwich / lmtest
  if (kind == "none") {
    fit <- lm(fml_rv, data = d)
    near(coef(fit), b, 1e-10, "lm coef")
    R <- switch(type,
      hc0 = vcovHC(fit, type = "HC0"), hc1 = vcovHC(fit, type = "HC1"),
      hc2 = vcovHC(fit, type = "HC2"), hc3 = vcovHC(fit, type = "HC3"),
      cluster = vcovCL(fit, cluster = d$region, type = "HC1"),
      hac = NeweyWest(fit, lag = L, order.by = d$week, prewhite = FALSE, adjust = TRUE))
    near(R, V, 1e-10, paste("sandwich", type))
    ct <- coeftest(fit, vcov. = V, df = df)
    near(ct[, 4], pv, 1e-10, paste("coeftest p", type))
    if (type == "cluster") {
      Rid <- vcovCL(fit, cluster = d$region_id, type = "HC1")
      near(Rid, V, 1e-10, "cluster by integer id")
    }
  }
  if (kind == "frequency" && type != "hac") {
    ed <- expand(d, w)
    fe <- lm(fml_rv, data = ed)
    R <- switch(type,
      hc0 = vcovHC(fe, type = "HC0"), hc1 = vcovHC(fe, type = "HC1"),
      hc2 = vcovHC(fe, type = "HC2"), hc3 = vcovHC(fe, type = "HC3"),
      cluster = vcovCL(fe, cluster = ed$region, type = "HC1"))
    near(R, V, 1e-9, paste("expanded sandwich", type))
  }
  if (kind != "none" && type == "hac") {
    d$.ws <- ws
    fw <- lm(fml_rv, data = d, weights = .ws)
    R <- NeweyWest(fw, lag = L, order.by = d$week, prewhite = FALSE, adjust = FALSE) * ns / (ns - k)
    near(R, V, 1e-10, paste("weighted NeweyWest", kind))
  }

  # Absent figures are omitted (a NULL list element would serialise as {}).
  rv_cases[[length(rv_cases) + 1]] <<- Filter(Negate(is.null), list(
    weight = kind, covariance = type,
    lags = if (is.null(lags)) (if (type == "hac") "auto" else NULL) else num(lags),
    lags_used = if (is.na(L)) NULL else num(L),
    clusters = if (is.na(G)) NULL else num(G),
    n_rows = num(nrow(d)), n_star = num(ns), df = num(df),
    terms = strs(colnames(X)), coefficients = numv(b), std_errors = numv(se),
    p_values = numv(pv), ci_lower = numv(b - q * se), ci_upper = numv(b + q * se),
    vcov = numm(V)
  ))
}
for (kind in c("none", "frequency", "probability")) {
  for (type in c("hc0", "hc1", "hc2", "hc3", "cluster")) rv_case(kind, type)
  rv_case(kind, "hac", 2)
  rv_case(kind, "hac")
}

rv_rows <- lapply(seq_len(nrow(rv)), function(i) list(
  week = num(rv$week[i]), region = rv$region[i], region_id = num(rv$region_id[i]),
  x1 = num(rv$x1[i]), x2 = num(rv$x2[i]), x3 = num(rv$x3[i]), y = num(rv$y[i]),
  w_freq = num(rv$w_freq[i]), w_prob = num(rv$w_prob[i])))

rv_doc <- c(rv_meta("robust regression coefficient covariance",
  "sandwich::vcovHC(lm, type = HC0..HC3), vcovCL(lm, cluster, type = 'HC1'), NeweyWest(lm, lag, order.by, prewhite = FALSE, adjust = TRUE); lmtest::coeftest(lm, vcov, df)",
  "Formula y ~ x1 + x2 + x3 over the rows below (shuffled; week orders the series, region / region_id are the same eight clusters). Terms use R's '(Intercept)' spelling. Weights follow the one rule (w* = w * N*/sum(w)); hc / cluster equal the sandwich calls on the rep()-expanded rows under frequency (asserted); hac reads each row as one period with score w* e x (== NeweyWest on lm(weights = w*) times N*/(N*-k), asserted). df: N* - k, or G - 1 under cluster; p_values = 2 pt(-|b/se|, df); ci at 0.95 on qt(0.975, df). lags: the request ('auto' or an integer), lags_used the resolved floor(4 (n/100)^(2/9)) over n rows."),
  list(rows = rv_rows, cases = rv_cases))
write_golden("mv_robust_vcov", rv_doc)
