# gen_robust_vcov_panel.R — R oracle for the panel series covariance
# estimators of REG_OLS, REG_GLM and REG_BAYES_LINEAR
# (RegressionSpec.Covariance: hac + entity = per-entity Newey-West,
# driscoll_kraay = Driscoll-Kraay; U69 E4-S1, #401).
#
# Writes <out_dir>/robust_vcov_panel.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   R_LIBS=<lib with plm> Rscript scripts/reference/gen_robust_vcov_panel.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. A rerun on
# the pinned toolchain is byte-identical; it draws its own seeded rows,
# so no other golden moves.
#
# Pinned packages: sandwich 3.1-3 (vcovPL), plm 2.6-7 (vcovNW, vcovSCC
# — the cross-check; with its imports bdsmatrix 1.3-7, collapse 2.1.9,
# maxLik 1.6-10, miscTools 0.6-30, recorded in the golden), jsonlite.
#
# Rows: a panel of six geos (geo categorical, geo_id the same entities
# as an integer) over weeks 1..14 with six (geo, week) cells dropped —
# unbalanced, with interior gaps — in SHUFFLED row order. A common week
# shock (cross-sectional dependence), an AR(1) term per geo (serial
# dependence) and heteroskedastic noise feed three responses: y
# (linear), y_bin (logit), y_count (log). Predictors x1, x2 (k = 3).
#
# Periods: the distinct weeks over the fitted rows, ranked 1..T (T =
# 14); a lag counts periods, so an entity's rows two weeks apart across
# a gap are lag 2. Unit scores g_i and bread B are those of
# gen_robust_vcov.R (OLS: e_i x_i, B = (X'W*X)^-1) and
# gen_robust_vcov_glm.R (GLM: me (y - mu)/V x at the polished MLE; Bayes:
# (y - x' mu_n) x, B = Lambda_n^-1); u_i = w*_i g_i on w* = w N*/sum(w).
#   hac + entity  M = sum_e sum_{i,j in e, |t_i - t_j| <= L}
#                 (1 - |t_i - t_j|/(L+1)) u_i u_j'
#   driscoll_kraay S_t = sum_{i in period t} u_i (across entities);
#                 M = sum_t S_t S_t' + sum_{l=1..L} (1 - l/(L+1))
#                 sum_t (S_t S_{t-l}' + S_{t-l} S_t')
# V = B M B N*/(N* - k). lags "auto" = floor(4 (T/100)^(2/9)) over the
# T periods (sandwich's lag = "NW1994"). OLS inference on N* - k df (t),
# GLM Wald z, Bayes normal intervals without p-values.
#
# Two-way cluster (U69 E4-S2, cluster2_cases): see the block above
# pv_rows — sandwich::vcovCL(cluster = ~geo + week / month) on the same
# rows (a `month` column joins them; the panel cases do not read it).
#
# Asserted: unweighted OLS == sandwich::vcovPL(lm, cluster = ~geo,
# order.by = ~week, aggregate = FALSE / TRUE, adjust = TRUE) AND
# plm::vcovNW / plm::vcovSCC(type = "HC1") on the pooling plm fit;
# unweighted GLM == vcovPL on glm refit from b; weighted (frequency,
# probability) == vcovPL(lm / glm(weights = w*), adjust = FALSE) *
# N*/(N* - k) (an entity-period's scores are one score, as the plain
# hac's); Bayes at prior precision 0 == vcovPL on lm.

suppressPackageStartupMessages(library(jsonlite))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
    message("wrote ", path)
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_robust_vcov_panel.R pins R 4.6.1, got ", R.version.string)
pinned_pv <- c(sandwich = "3.1.3", plm = "2.6.7", bdsmatrix = "1.3.7", collapse = "2.1.9",
               maxLik = "1.6.10", miscTools = "0.6.30")
for (p in names(pinned_pv)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != pinned_pv[[p]]) stop(sprintf("package %s is %s, gen_robust_vcov_panel.R is pinned to %s (see the header)", p, have, pinned_pv[[p]]))
}
suppressPackageStartupMessages({
  library(sandwich)
  library(plm)
})

pv_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
pv_num <- function(x) {
  if (length(x) != 1 || !is.finite(x)) stop("pv_num() takes a finite scalar")
  structure(sprintf("%.17g", x), class = "json")
}
pv_g17n <- function(v) {
  v <- unname(as.numeric(v))
  if (any(is.infinite(v))) stop("infinite reference value")
  structure(paste0("[", paste(ifelse(is.nan(v) | is.na(v), "null", sprintf("%.17g", v)), collapse = ","), "]"), class = "json")
}
pv_mat <- function(m) structure(paste0("[", paste(vapply(seq_len(nrow(m)), function(i) unclass(pv_g17(m[i, ])), ""), collapse = ","), "]"), class = "json")
pv_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (any(abs(a - b) > tol * pmax(1, abs(a), abs(b)))) stop("self-check failed: ", what, " max diff ", max(abs(a - b)))
}

set.seed(20261123)
pv_geos <- paste0("geo_", LETTERS[1:6])
pv <- expand.grid(week = 1:14, geo_id = 1:6)
pv$geo <- pv_geos[pv$geo_id]
pv <- pv[-c(3, 20, 21, 45, 60, 83), ]
n_pv <- nrow(pv)
week_shock <- rnorm(14, 0, 0.6)
geo_eff <- rnorm(6, 0, 0.4)
pv$x1 <- round(rnorm(n_pv) + 0.4 * week_shock[pv$week], 3)
pv$x2 <- round(runif(n_pv, 0, 2), 3)
ar <- numeric(n_pv)
for (g in 1:6) {
  ix <- which(pv$geo_id == g)
  a <- rnorm(1, 0, 0.4)
  for (j in seq_along(ix)) {
    if (j > 1) a <- 0.6 * a + rnorm(1, 0, 0.4)
    ar[ix[j]] <- a
  }
}
lat <- week_shock[pv$week] + geo_eff[pv$geo_id] + ar
pv$y <- round(1 + 1.2 * pv$x1 - 0.7 * pv$x2 + lat + rnorm(n_pv, 0, 0.3 + 0.3 * pv$x2), 3)
pv$y_bin <- rbinom(n_pv, 1, plogis(-0.1 + 0.8 * pv$x1 - 0.5 * pv$x2 + lat))
pv$y_count <- rpois(n_pv, exp(0.4 + 0.3 * pv$x1 + 0.2 * pv$x2 + 0.6 * lat))
pv$w_freq <- sample(1:4, n_pv, replace = TRUE)
pv$w_prob <- round(runif(n_pv, 0.2, 3), 3)
pv <- pv[sample(n_pv), ]
rownames(pv) <- NULL

pv_auto <- function(n) floor(4 * (n / 100)^(2 / 9))

# pv_meat is the panel meat over scores U (= w* g, rows of d).
pv_meat <- function(type, U, d, L) {
  per <- sort(unique(d$week))
  tr <- match(d$week, per)
  k <- ncol(U)
  if (type == "driscoll_kraay") {
    S <- matrix(0, length(per), k)
    for (i in seq_len(nrow(U))) S[tr[i], ] <- S[tr[i], ] + U[i, ]
    M <- crossprod(S)
    for (l in seq_len(L)) {
      if (l >= nrow(S)) break
      Gl <- crossprod(S[(l + 1):nrow(S), , drop = FALSE], S[1:(nrow(S) - l), , drop = FALSE])
      M <- M + (1 - l / (L + 1)) * (Gl + t(Gl))
    }
    return(M)
  }
  M <- matrix(0, k, k)
  for (i in seq_len(nrow(U))) for (j in seq_len(nrow(U))) {
    if (d$geo[i] != d$geo[j]) next
    l <- abs(tr[i] - tr[j])
    if (l <= L) M <- M + (1 - l / (L + 1)) * outer(U[i, ], U[j, ])
  }
  M
}

pv_glm <- function(X, y, ws, fam) {
  ctl <- glm.control(epsilon = 1e-14, maxit = 200)
  b <- suppressWarnings(glm.fit(X, y, weights = ws, family = fam, control = ctl))$coefficients
  last <- Inf
  for (it in 1:100) {
    eta <- drop(X %*% b); mu <- fam$linkinv(eta); me <- fam$mu.eta(eta)
    w <- ws * me^2 / fam$variance(mu)
    step <- drop(solve(crossprod(X * w, X), crossprod(X * w, (y - mu) / me)))
    rel <- max(abs(step) / pmax(abs(b), 1))
    b <- b + step
    if (rel == 0 || (rel <= 1e-14 && rel >= last)) break
    last <- rel
  }
  if (rel > 1e-14) stop("glm fixed point not reached: relative step ", rel)
  eta <- drop(X %*% b); mu <- fam$linkinv(eta); me <- fam$mu.eta(eta); va <- fam$variance(mu)
  list(b = b, B = solve(crossprod(X * (ws * me^2 / va), X)), G = X * (me * (y - mu) / va))
}
pv_bayes <- function(X, y, ws, eps) {
  Linv <- solve(crossprod(X * ws, X) + diag(eps, ncol(X)))
  mun <- drop(Linv %*% crossprod(X * ws, y))
  list(b = mun, B = Linv, G = X * drop(y - X %*% mun))
}
pv_ols <- function(X, y, ws) {
  B <- solve(crossprod(X * ws, X))
  b <- drop(B %*% crossprod(X * ws, y))
  list(b = b, B = B, G = X * drop(y - X %*% b))
}
pv_r <- function(fit, type, d, L, adjust) {
  vcovPL(fit, cluster = d$geo, order.by = d$week, aggregate = type == "driscoll_kraay", lag = L, adjust = adjust)
}

pv_cases <- list()
pv_case <- function(family, kind, type, lags = NULL) {
  d <- pv
  target <- switch(family, ols = "y", bayes = "y", binomial = "y_bin", poisson = "y_count")
  w <- switch(kind, none = rep(1, nrow(d)), frequency = d$w_freq, probability = d$w_prob)
  ns <- if (kind == "probability") sum(w)^2 / sum(w^2) else sum(w)
  ws <- w * ns / sum(w)
  X <- model.matrix(~ x1 + x2, d); yv <- d[[target]]; k <- ncol(X)
  fam <- switch(family, binomial = binomial(), poisson = poisson(), NULL)
  f <- switch(family, ols = pv_ols(X, yv, ws), bayes = pv_bayes(X, yv, ws, 1e-3), pv_glm(X, yv, ws, fam))
  periods <- length(unique(d$week))
  L <- if (is.null(lags)) pv_auto(periods) else lags
  V <- f$B %*% pv_meat(type, f$G * ws, d, L) %*% f$B * ns / (ns - k)
  b <- f$b
  dimnames(V) <- list(colnames(X), colnames(X))
  se <- sqrt(diag(V))
  if (family == "ols") {
    df <- ns - k; q <- qt(0.975, df); pval <- 2 * pt(-abs(b / se), df)
  } else {
    df <- NULL; q <- qnorm(0.975); pval <- 2 * pnorm(-abs(b / se))
  }
  fml <- reformulate(c("x1", "x2"), target)
  ctl <- glm.control(epsilon = 1e-14, maxit = 200)

  # --- self-checks against sandwich::vcovPL and plm
  if (family == "ols" && kind == "none") {
    fit <- lm(fml, data = d)
    pv_near(coef(fit), b, 1e-10, "lm coef")
    pv_near(pv_r(fit, type, d, L, TRUE), V, 1e-10, paste("vcovPL lm", type))
    pm <- plm(fml, data = pdata.frame(d, index = c("geo", "week")), model = "pooling")
    R <- if (type == "driscoll_kraay") vcovSCC(pm, maxlag = L, type = "HC1") else vcovNW(pm, maxlag = L, type = "HC1")
    pv_near(unclass(R)[colnames(X), colnames(X)], V, 1e-10, paste("plm", type))
  }
  if (family %in% c("binomial", "poisson") && kind == "none") {
    fit <- glm(fml, family = fam, data = d, start = b, control = ctl)
    pv_near(pv_r(fit, type, d, L, TRUE), V, 1e-8, paste("vcovPL glm", family, type))
  }
  if (family %in% c("ols", "binomial", "poisson") && kind != "none") {
    d$.ws <- ws
    fw <- if (family == "ols") lm(fml, data = d, weights = .ws) else
      suppressWarnings(glm(fml, family = fam, data = d, weights = .ws, start = b, control = ctl))
    pv_near(pv_r(fw, type, d, L, FALSE) * ns / (ns - k), V, 1e-8, paste("weighted vcovPL", family, kind, type))
  }
  if (family == "bayes" && kind == "none") {
    f0 <- pv_bayes(X, yv, ws, 0)
    V0 <- f0$B %*% pv_meat(type, f0$G * ws, d, L) %*% f0$B * ns / (ns - k)
    pv_near(pv_r(lm(fml, data = d), type, d, L, TRUE), V0, 1e-10, paste("bayes precision 0 vs vcovPL", type))
  }

  pv_cases[[length(pv_cases) + 1]] <<- Filter(Negate(is.null), list(
    family = family, target = target, weight = kind, covariance = type, entity = "geo",
    lags = if (is.null(lags)) "auto" else pv_num(lags),
    lags_used = pv_num(L), periods = pv_num(periods), entities = pv_num(length(unique(d$geo))),
    n_star = pv_num(ns), df = if (is.null(df)) NULL else pv_num(df),
    terms = I(colnames(X)), coefficients = pv_g17(b), std_errors = pv_g17(se),
    p_values = if (family == "bayes") NULL else pv_g17(pval),
    ci_lower = pv_g17(b - q * se), ci_upper = pv_g17(b + q * se),
    vcov = pv_mat(V)
  ))
}
for (family in c("ols", "binomial", "poisson", "bayes")) {
  kinds <- if (family == "bayes") c("none", "frequency") else c("none", "frequency", "probability")
  for (kind in kinds) for (type in c("hac", "driscoll_kraay")) {
    pv_case(family, kind, type, 3)
    pv_case(family, kind, type)
  }
}

# --- Two-way clustering (U69 E4-S2): Cameron-Gelbach-Miller over the
# same rows, no new draws (so the panel cases above are unchanged).
# Pairs: geo x week (every intersection cell one row, G12 = n) and geo x
# month, month = (week - 1) %/% 4 + 1 (four levels; cells of up to four
# rows). With M_c = sum over the levels of c of s s', s = sum w* g:
#   V = B [G1/(G1-1) M_1 + G2/(G2-1) M_2 - G12/(G12-1) M_12] B (N*-1)/(N*-k)
# = sandwich::vcovCL(cluster = ~a + b, type = "HC1", cadjust = TRUE,
# multi0 = FALSE) unweighted; weighted == vcovCL(lm / glm(weights = w*),
# type = "HC0") * (N*-1)/(N*-k) (vcovCL's HC1 factor counts rows), plus a
# frequency-weighted OLS check on the rep()-expanded rows. OLS inference
# on min(G1, G2) - 1 df (t, the one-way G - 1 rule's two-way form), GLM
# Wald z, Bayes normal intervals without p-values.
pv$month <- (pv$week - 1) %/% 4 + 1
pv_cl_meat <- function(U, key) crossprod(rowsum(U, key, reorder = TRUE))
pv_cl2_cases <- list()
pv_cl2_case <- function(family, kind, c2) {
  d <- pv
  target <- switch(family, ols = "y", bayes = "y", binomial = "y_bin", poisson = "y_count")
  w <- switch(kind, none = rep(1, nrow(d)), frequency = d$w_freq, probability = d$w_prob)
  ns <- if (kind == "probability") sum(w)^2 / sum(w^2) else sum(w)
  ws <- w * ns / sum(w)
  X <- model.matrix(~ x1 + x2, d); yv <- d[[target]]; k <- ncol(X)
  fam <- switch(family, binomial = binomial(), poisson = poisson(), NULL)
  f <- switch(family, ols = pv_ols(X, yv, ws), bayes = pv_bayes(X, yv, ws, 1e-3), pv_glm(X, yv, ws, fam))
  k1 <- d$geo; k2 <- d[[c2]]; k12 <- paste(k1, k2, sep = "_")
  g <- c(length(unique(k1)), length(unique(k2)), length(unique(k12)))
  a <- g / (g - 1)
  U <- f$G * ws
  M <- a[1] * pv_cl_meat(U, k1) + a[2] * pv_cl_meat(U, k2) - a[3] * pv_cl_meat(U, k12)
  V <- f$B %*% M %*% f$B * (ns - 1) / (ns - k)
  b <- f$b
  dimnames(V) <- list(colnames(X), colnames(X))
  # The CGM meat is a difference: a variance can come out negative (few
  # clusters). vcovCL(fix = FALSE) keeps it; its SE, p-value and interval
  # are NaN here and null in the golden (pv_g17n).
  se <- suppressWarnings(sqrt(diag(V)))
  if (family == "ols") {
    df <- min(g[1:2]) - 1; q <- qt(0.975, df); pval <- 2 * pt(-abs(b / se), df)
  } else {
    df <- NULL; q <- qnorm(0.975); pval <- 2 * pnorm(-abs(b / se))
  }
  fml <- reformulate(c("x1", "x2"), target)
  ctl <- glm.control(epsilon = 1e-14, maxit = 200)
  cl <- data.frame(a = k1, b = k2)

  # --- self-checks against sandwich::vcovCL (two-way)
  if (family == "ols" && kind == "none") {
    fit <- lm(fml, data = d)
    pv_near(vcovCL(fit, cluster = cl, type = "HC1"), V, 1e-10, paste("vcovCL two-way lm", c2))
    d$.k2 <- k2
    pv_near(vcovCL(fit, cluster = ~ geo + .k2, type = "HC1"), V, 1e-10, paste("vcovCL two-way formula", c2))
    ct <- lmtest::coeftest(fit, vcov. = V, df = df)
    pv_near(ct[, 4], pval, 1e-10, paste("coeftest two-way", c2))
  }
  if (family %in% c("binomial", "poisson") && kind == "none") {
    fit <- glm(fml, family = fam, data = d, start = b, control = ctl)
    pv_near(vcovCL(fit, cluster = cl, type = "HC1"), V, 1e-8, paste("vcovCL two-way glm", family, c2))
  }
  if (family %in% c("ols", "binomial", "poisson") && kind != "none") {
    d$.ws <- ws
    fw <- if (family == "ols") lm(fml, data = d, weights = .ws) else
      suppressWarnings(glm(fml, family = fam, data = d, weights = .ws, start = b, control = ctl))
    pv_near(vcovCL(fw, cluster = cl, type = "HC0") * (ns - 1) / (ns - k), V, 1e-8, paste("weighted vcovCL two-way", family, kind, c2))
  }
  if (family == "ols" && kind == "frequency") {
    ix <- rep(seq_len(nrow(d)), d$w_freq)
    ed <- d[ix, ]
    fe <- lm(fml, data = ed)
    pv_near(vcovCL(fe, cluster = data.frame(a = ed$geo, b = ed[[c2]]), type = "HC1"), V, 1e-9, paste("expanded vcovCL two-way", c2))
  }
  if (family == "bayes" && kind == "none") {
    f0 <- pv_bayes(X, yv, ws, 0)
    U0 <- f0$G * ws
    M0 <- a[1] * pv_cl_meat(U0, k1) + a[2] * pv_cl_meat(U0, k2) - a[3] * pv_cl_meat(U0, k12)
    V0 <- f0$B %*% M0 %*% f0$B * (ns - 1) / (ns - k)
    pv_near(vcovCL(lm(fml, data = d), cluster = cl, type = "HC1"), V0, 1e-10, paste("bayes precision 0 vs vcovCL two-way", c2))
  }

  pv_cl2_cases[[length(pv_cl2_cases) + 1]] <<- Filter(Negate(is.null), list(
    family = family, target = target, weight = kind, cluster = "geo", cluster2 = c2,
    clusters = pv_num(g[1]), clusters2 = pv_num(g[2]), intersections = pv_num(g[3]),
    n_star = pv_num(ns), df = if (is.null(df)) NULL else pv_num(df),
    terms = I(colnames(X)), coefficients = pv_g17(b), std_errors = pv_g17n(se),
    negative_variances = pv_num(sum(diag(V) < 0)),
    p_values = if (family == "bayes") NULL else pv_g17n(pval),
    ci_lower = pv_g17n(b - q * se), ci_upper = pv_g17n(b + q * se),
    vcov = pv_mat(V)
  ))
}
for (family in c("ols", "binomial", "poisson", "bayes")) {
  kinds <- if (family == "bayes") c("none", "frequency") else c("none", "frequency", "probability")
  for (kind in kinds) for (c2 in c("week", "month")) pv_cl2_case(family, kind, c2)
}

pv_rows <- lapply(seq_len(nrow(pv)), function(i) list(
  week = pv_num(pv$week[i]), month = pv_num(pv$month[i]), geo = pv$geo[i], geo_id = pv_num(pv$geo_id[i]),
  x1 = pv_num(pv$x1[i]), x2 = pv_num(pv$x2[i]), y = pv_num(pv$y[i]),
  y_bin = pv_num(pv$y_bin[i]), y_count = pv_num(pv$y_count[i]),
  w_freq = pv_num(pv$w_freq[i]), w_prob = pv_num(pv$w_prob[i])))

pv_pkgs <- c("jsonlite", "stats", "sandwich", "plm", "bdsmatrix", "collapse", "maxLik", "miscTools", "zoo", "nlme", "Formula", "Rdpack", "lmtest")
write_golden("robust_vcov_panel", list(
  generator = "scripts/reference/gen_robust_vcov_panel.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = setNames(lapply(pv_pkgs, function(p) as.character(packageVersion(p))), pv_pkgs),
  primitive = "panel series coefficient covariance: per-entity Newey-West (hac + entity) and Driscoll-Kraay; two-way cluster (cluster2_cases)",
  r_function = "sandwich::vcovPL(fit, cluster = geo, order.by = week, aggregate = FALSE (hac + entity) / TRUE (driscoll_kraay), lag, adjust); plm::vcovNW / vcovSCC(pooling plm, maxlag, type = 'HC1') cross-check",
  note = "family ols (y ~ x1 + x2, REG_OLS, t on N* - k df), binomial (y_bin, logit), poisson (y_count, log) — Wald z — or bayes (y, REG_BAYES_LINEAR, default prior, normal intervals, no p-values). entity geo (six levels; geo_id the same as an integer), order_by week (14 periods, unbalanced with gaps; a lag counts periods). Weights follow the one rule (w* = w N*/sum(w)). lags: the request, lags_used the resolved floor(4 (T/100)^(2/9)) over the T periods. Terms use R's '(Intercept)' spelling.",
  cases = pv_cases,
  cluster2_note = "two-way cluster (covariance cluster geo + cluster2 week or month, month = (week - 1) %/% 4 + 1): V = B [G1/(G1-1) M_1 + G2/(G2-1) M_2 - G12/(G12-1) M_12] B (N*-1)/(N*-k), M_c the cluster meat over c's levels (G12 the intersection cells); == sandwich::vcovCL(cluster = ~geo + b, type = 'HC1', multi0 = FALSE) unweighted, vcovCL(weights = w*, type = 'HC0') * (N*-1)/(N*-k) weighted. OLS t on min(G1, G2) - 1 df; GLM Wald z; Bayes normal, no p-values.",
  cluster2_cases = pv_cl2_cases,
  rows = pv_rows
))
