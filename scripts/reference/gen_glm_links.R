# gen_glm_links.R — R oracle for the non-canonical REG_GLM links (U65
# E2-S1, #625): binomial probit / cloglog, poisson identity / sqrt,
# gamma log / identity.
#
# Writes <out_dir>/glm_links.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_glm_links.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite, plus sandwich 3.1-3 for the HC figures.
#
# Per case, y ~ x1 + x2 by glm(family = <family>(link = <link>),
# glm.control(epsilon = 1e-14, maxit = 200)) refitted twice from its own
# optimum (glm_exact: glm keeps the working weights of the iteration
# BEFORE its last update, so vcov / hatvalues lag the converged mu
# without the refit). Pinned, every figure re-derived below from its
# closed form at the converged eta (mu = g^-1(eta), me = dmu/deta,
# V(mu), W = w me^2 / V the IRLS working weight) and the script stops on
# a mismatch, so the golden also pins the formulas Pulse implements:
#   coefficients, std_errors, vcov  summary.glm: phi (X'WX)^-1 (the
#                                   expected information R's IRLS reads,
#                                   never the observed one), phi = 1
#                                   binomial / poisson, the Pearson
#                                   chi-square / df.residual for gamma
#   deviance, null_deviance         sum of dev.resids; null at mu = the
#                                   weighted mean of y (the intercept-
#                                   only MLE under any link)
#   aic                             AIC(m): family$aic + 2 rank (gamma's
#                                   family$aic counts its dispersion)
#   dispersion, df_residual         summary(m)$dispersion, m$df.residual
#   hc0 / hc1 / hc3 std errors      sandwich::vcovHC (bread (X'WX)^-1,
#                                   score me (y - mu)/V x — the working
#                                   residual times the working weight;
#                                   unweighted cases only)
#   per row (unweighted cases)      fitted mu, the working residual
#                                   (y - mu) / me = (y - mu) g'(mu),
#                                   hatvalues, dffits (whose sign is
#                                   sign(g'(mu)): + under every link here)
# The frequency-weighted cases ("_freq") are glm(weights = f) and their
# figures are the rep()-expanded glm's (the one weighting rule): the
# script asserts the weighted coefficients and deviances equal the
# expansion's and pins the expansion's summary (its df.residual = sum(f)
# - k, which glm(weights = f) would not read).
#
# Seeded (set.seed(20261104)); draws its own rows after every other
# golden, so no earlier golden moves. Rows are [x1, x2, y] or
# [x1, x2, y, f].

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(sandwich))

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_glm_links.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("sandwich")) != "3.1.3") stop("gen_glm_links.R pins sandwich 3.1-3, got ", packageVersion("sandwich"))

gl_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
gl_mat <- function(m) lapply(seq_len(nrow(m)), function(i) gl_g17(m[i, ]))
gl_check <- function(what, got, want) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (max(abs(got - want) / pmax(1, abs(want))) > 1e-10) stop(what, ": closed form disagrees with R")
}

gl_ctl <- glm.control(epsilon = 1e-14, maxit = 200)
# Under a non-canonical link IRLS is Fisher scoring, not Newton, so it
# converges only linearly, while glm's deviance test stops once the
# deviance (quadratic in the coefficient error) stalls: a glm fit sits
# ~1e-9 off its MLE. Each refit from start = coef(m) is one more scoring
# step; refit until the coefficients reach their fixed point (relative
# 1e-14, round-off past that), so every figure below is evaluated at
# the MLE.
gl_exact <- function(fam, data, weights = NULL) {
  data$.f <- if (is.null(weights)) rep(1, nrow(data)) else weights
  m <- glm(y ~ x1 + x2, family = fam, data = data, weights = .f, control = gl_ctl)
  for (k in 1:500) {
    b <- coef(m)
    m <- glm(y ~ x1 + x2, family = fam, data = data, weights = .f, control = gl_ctl, start = b)
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-14) break
  }
  if (k == 500) stop("glm did not reach its fixed point")
  m <- glm(y ~ x1 + x2, family = fam, data = data, weights = .f, control = gl_ctl, start = coef(m))
  if (!m$converged) stop("glm did not converge")
  m
}

# gl_figures re-derives and returns the fit-level figures of m.
gl_figures <- function(name, m) {
  fam <- m$family
  X <- model.matrix(m)
  y <- m$y
  pw <- m$prior.weights
  eta <- m$linear.predictors
  mu <- fam$linkinv(eta)
  gl_check(paste(name, "fitted"), mu, fitted(m))
  me <- fam$mu.eta(eta)
  V <- fam$variance(mu)
  W <- pw * me^2 / V
  gl_check(paste(name, "working weights"), W, weights(m, "working"))
  inv <- solve(t(X) %*% (W * X))
  s <- summary(m)
  phi <- s$dispersion
  est <- !(fam$family %in% c("binomial", "poisson"))
  if (est) gl_check(paste(name, "dispersion"), phi, sum(pw * (y - mu)^2 / V) / m$df.residual) else if (phi != 1) stop(name, ": fixed dispersion is not 1")
  gl_check(paste(name, "vcov"), phi * inv, vcov(m))
  gl_check(paste(name, "deviance"), sum(fam$dev.resids(y, mu, pw)), deviance(m))
  wtdmu <- sum(pw * y) / sum(pw)
  gl_check(paste(name, "null deviance"), sum(fam$dev.resids(y, wtdmu, pw)), m$null.deviance)
  # Gamma()$aic already adds 2 for the dispersion.
  aic <- fam$aic(y, rep(1, length(y)), mu, pw, deviance(m)) + 2 * m$rank
  gl_check(paste(name, "aic"), aic, AIC(m))
  list(
    coefficients = gl_g17(coef(m)), std_errors = gl_g17(sqrt(diag(vcov(m)))), vcov = gl_mat(vcov(m)),
    deviance = gl_g17(deviance(m)), null_deviance = gl_g17(m$null.deviance), aic = gl_g17(AIC(m)),
    dispersion = gl_g17(phi), df_residual = gl_g17(m$df.residual),
    X = X, y = y, eta = eta, mu = mu, me = me, V = V, W = W, inv = inv, phi = phi, est = est)
}

gl_cases <- list()

gl_case <- function(name, family, link, d) {
  fam <- switch(family, binomial = binomial(link = link), poisson = poisson(link = link), gamma = Gamma(link = link))
  m <- gl_exact(fam, d)
  if (m$family$link != link) stop(name, ": wrong link")
  f <- gl_figures(name, m)
  # Per row.
  wr <- (f$y - f$mu) / f$me
  gl_check(paste(name, "working residual"), wr, residuals(m, type = "working"))
  hat <- rowSums((f$X %*% f$inv) * f$X) * f$W
  gl_check(paste(name, "hat"), hat, hatvalues(m))
  pres <- (f$y - f$mu) / sqrt(f$V)
  n <- nrow(d); p1 <- m$rank; dfr <- n - p1
  sig <- if (f$est) sqrt((sum(pres^2) - pres^2 / (1 - hat)) / (dfr - 1)) else sqrt(f$phi)
  dff <- sign(f$me) * pres * sqrt(hat) / (sig * (1 - hat))
  gl_check(paste(name, "dffits"), dff, dffits(m))
  # HC sandwich: bread (X'WX)^-1, unit score me (y - mu)/V x.
  sc <- (f$me * (f$y - f$mu) / f$V) * f$X
  hc <- function(type) {
    omega <- switch(type, HC0 = 1, HC1 = 1, HC3 = 1 / (1 - hat)^2)
    v <- f$inv %*% t(sc) %*% (omega * sc) %*% f$inv
    if (type == "HC1") v <- v * n / (n - p1)
    gl_check(paste(name, type), v, vcovHC(m, type = type))
    sqrt(diag(v))
  }
  out <- c(list(name = name, family = family, link = link, weighted = FALSE,
    rows = lapply(seq_len(n), function(i) gl_g17(c(d$x1[i], d$x2[i], d$y[i])))),
    f[c("coefficients", "std_errors", "vcov", "deviance", "null_deviance", "aic", "dispersion", "df_residual")],
    list(hc0_std_errors = gl_g17(hc("HC0")), hc1_std_errors = gl_g17(hc("HC1")), hc3_std_errors = gl_g17(hc("HC3")),
      fitted = gl_g17(f$mu), working = gl_g17(wr), hat = gl_g17(hat), dffits = gl_g17(dff)))
  gl_cases[[length(gl_cases) + 1]] <<- out
}

# A frequency-weighted case: glm(weights = f) against the rep() expansion.
gl_case_freq <- function(name, family, link, d, f) {
  fam <- switch(family, binomial = binomial(link = link), poisson = poisson(link = link), gamma = Gamma(link = link))
  mw <- gl_exact(fam, d, f)
  ex <- d[rep(seq_len(nrow(d)), f), ]
  me <- gl_exact(fam, ex)
  gl_check(paste(name, "weighted vs expanded coefficients"), coef(mw), coef(me))
  gl_check(paste(name, "weighted vs expanded deviance"), deviance(mw), deviance(me))
  gl_check(paste(name, "weighted vs expanded null deviance"), mw$null.deviance, me$null.deviance)
  fe <- gl_figures(paste(name, "expanded"), me)
  # Per distinct row: the fitted mean and the working residual, the
  # same for each copy.
  eta <- drop(model.matrix(mw) %*% coef(me))
  mu <- mw$family$linkinv(eta)
  wr <- (d$y - mu) / mw$family$mu.eta(eta)
  gl_check(paste(name, "working residual"), wr, residuals(mw, type = "working"))
  out <- c(list(name = name, family = family, link = link, weighted = TRUE,
    rows = lapply(seq_len(nrow(d)), function(i) gl_g17(c(d$x1[i], d$x2[i], d$y[i], f[i])))),
    fe[c("coefficients", "std_errors", "vcov", "deviance", "null_deviance", "aic", "dispersion", "df_residual")],
    list(fitted = gl_g17(mu), working = gl_g17(wr)))
  gl_cases[[length(gl_cases) + 1]] <<- out
}

set.seed(20261104)
gl_design <- function(n) data.frame(x1 = round(runif(n, 0, 2), 3), x2 = round(runif(n, -1, 1), 3))

d <- gl_design(60)
d$y <- as.numeric(runif(60) < pnorm(-0.4 + 0.7 * d$x1 - 0.6 * d$x2))
gl_case("binomial_probit", "binomial", "probit", d)

d <- gl_design(60)
d$y <- as.numeric(runif(60) < 1 - exp(-exp(-0.9 + 0.6 * d$x1 + 0.5 * d$x2)))
gl_case("binomial_cloglog", "binomial", "cloglog", d)

d <- gl_design(50)
d$y <- rpois(50, 3 + 1.2 * d$x1 - 0.8 * d$x2)
gl_case("poisson_identity", "poisson", "identity", d)

d <- gl_design(50)
d$y <- rpois(50, (1.5 + 0.4 * d$x1 - 0.3 * d$x2)^2)
gl_case("poisson_sqrt", "poisson", "sqrt", d)

d <- gl_design(50)
mu <- exp(0.5 + 0.4 * d$x1 - 0.3 * d$x2)
d$y <- round(rgamma(50, shape = 3, rate = 3 / mu), 4)
gl_case("gamma_log", "gamma", "log", d)

d <- gl_design(50)
mu <- 2 + 0.8 * d$x1 - 0.5 * d$x2
d$y <- round(rgamma(50, shape = 5, rate = 5 / mu), 4)
gl_case("gamma_identity", "gamma", "identity", d)

# Frequency weights, one link per family.
d <- gl_design(30)
d$y <- as.numeric(runif(30) < pnorm(-0.2 + 0.5 * d$x1 + 0.7 * d$x2))
gl_case_freq("binomial_probit_freq", "binomial", "probit", d, sample(1:4, 30, replace = TRUE))

d <- gl_design(30)
d$y <- rpois(30, (1.2 + 0.5 * d$x1 + 0.3 * d$x2)^2)
gl_case_freq("poisson_sqrt_freq", "poisson", "sqrt", d, sample(1:4, 30, replace = TRUE))

d <- gl_design(30)
mu <- exp(0.3 + 0.5 * d$x1 - 0.4 * d$x2)
d$y <- round(rgamma(30, shape = 4, rate = 4 / mu), 4)
gl_case_freq("gamma_log_freq", "gamma", "log", d, sample(1:4, 30, replace = TRUE))

write_golden("glm_links", list(
  generator = "scripts/reference/gen_glm_links.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
                  sandwich = as.character(packageVersion("sandwich"))),
  note = paste(
    "y ~ x1 + x2 by glm(family(link)), epsilon 1e-14, refitted twice from its optimum; terms (intercept), x1, x2.",
    "rows are [x1, x2, y] or [x1, x2, y, f]; per-row figures in row order.",
    "_freq cases: glm(weights = f), every fit-level figure the rep()-expanded glm's; fitted / working per distinct row.",
    "hc*_std_errors: sandwich::vcovHC (unweighted cases)."),
  cases = gl_cases
))
