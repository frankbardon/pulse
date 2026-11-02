# gen_glm_nb.R — R oracle for the REG_GLM negative binomial family (NB2,
# Var = mu + mu^2 / theta; U65 E2-S2, #626): theta estimated by maximum
# likelihood as MASS::glm.nb does, or fixed as
# glm(family = MASS::negative.binomial(theta)).
#
# Writes <out_dir>/glm_nb.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_glm_nb.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite, MASS 7.3-66 (glm.nb, theta.ml, negative.binomial)
# and sandwich 3.1-3 for the HC figures.
#
# The estimator is glm.nb's: alternate IRLS for beta at fixed theta with
# theta.ml (Newton on the theta score) at fixed mu. glm.nb's own
# convergence is loose — theta.ml stops once a Newton step is below
# eps^0.25 ~ 1.2e-4 ABSOLUTE and the alternation on a log-likelihood
# change of 1e-8 — so each case runs glm.nb first, then pushes the same
# alternation to its fixed point with MASS's own pieces (glm.fit under
# negative.binomial(theta) refitted from its own optimum, theta.ml with
# eps 1e-12 * theta), until beta and theta move by under 1e-13 relative.
# The glm.nb fit is kept as a cross-check (within 1e-5): the golden is
# the joint MLE glm.nb approximates. The fixed-theta case is
# glm(family = negative.binomial(theta)) pushed to its fixed point.
#
# Every figure is re-derived from its closed form at the converged eta
# (mu = g^-1(eta), me = dmu/deta, V = mu + mu^2/theta, W = w me^2 / V)
# and the script stops on a mismatch:
#   coefficients, std_errors, vcov  summary.negbin: (X'WX)^-1, dispersion
#                                   1 at theta-hat (beta and theta treated
#                                   as orthogonal); the fixed-theta case
#                                   too (summary(m, dispersion = 1) —
#                                   summary.glm would estimate a
#                                   dispersion for a family that carries
#                                   none, which Pulse does not)
#   theta                           score(theta) = 0 (theta.ml's score)
#   theta_std_err                   1 / sqrt(info(theta-hat)), theta.ml's
#                                   SE at the converged theta
#   two_log_lik                     2 sum w [lgamma(theta + y) -
#                                   lgamma(theta) - lgamma(y + 1) +
#                                   theta log theta + y log(mu + (y == 0))
#                                   - (theta + y) log(theta + mu)]
#                                   (glm.nb's loglik), = -family$aic
#   aic                             -two_log_lik + 2 rank + 2 when theta
#                                   is estimated (glm.nb's aic, AIC on its
#                                   logLik.negbin df rank + 1); + 2 rank
#                                   only when fixed (AIC(glm))
#   deviance, null_deviance         sum of dev.resids at theta-hat; null
#                                   at mu = the weighted mean of y
#   hc0 / hc1 / hc3 std errors      sandwich::vcovHC: bread (X'WX)^-1,
#                                   score me (y - mu) / V x at theta-hat
#                                   — theta held fixed, the treatment
#                                   Pulse's sandwich applies (sandwich's
#                                   bread.glm / estfun.glm fix the
#                                   dispersion at 1 for a "Negative
#                                   Binomial" family), so they agree
#   per row (unweighted)            fitted mu, residuals(type =)
#                                   pearson / deviance / working,
#                                   hatvalues
#   influence case                  rstandard (pearson, deviance) and
#                                   cooks.distance on the negbin fit
#                                   (sigma / summary dispersion 1), and
#                                   rstudent / dffits at dispersion 1:
#                                   stats:::estDisp is TRUE for a family
#                                   whose dispersion slot is NULL, so on
#                                   a bare negbin fit those two would read
#                                   lm.influence's leave-one-out sigma,
#                                   against summary.negbin's phi = 1; the
#                                   golden takes them with
#                                   family$dispersion <- 1 (R's own fixed-
#                                   dispersion flag), the phi = 1 forms
#                                   Pulse reads (rstudent = the Pearson
#                                   rstandard, dffits at sigma 1)
# The "_disp" cases fix theta with the dispersion ESTIMATED (Pulse
# dispersion "estimated"; see nb_disp_figures below): summary.glm's
# phi-hat, its t inference and the influence measures on the bare fit.
# The frequency-weighted case ("_freq") is glm.nb(weights = f) and its
# figures are the rep()-expanded fit's (the one weighting rule): the
# script asserts the weighted coefficients, theta, deviances and
# log-likelihood equal the expansion's and pins the expansion's summary.
#
# Seeded (set.seed(20261105)); draws its own rows after every other
# golden, so no earlier golden moves. Rows are [x1, x2, y] or
# [x1, x2, y, f].

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(MASS))
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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_glm_nb.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("MASS")) != "7.3.66") stop("gen_glm_nb.R pins MASS 7.3-66, got ", packageVersion("MASS"))
if (as.character(packageVersion("sandwich")) != "3.1.3") stop("gen_glm_nb.R pins sandwich 3.1-3, got ", packageVersion("sandwich"))

nb_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
nb_mat <- function(m) lapply(seq_len(nrow(m)), function(i) nb_g17(m[i, ]))
nb_check <- function(what, got, want, tol = 1e-10) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  if (max(abs(got - want) / pmax(1, abs(want))) > tol) stop(what, ": closed form disagrees with R")
}

nb_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# The theta score and information (theta.ml's), and glm.nb's loglik.
nb_score <- function(th, mu, y, w) sum(w * (digamma(th + y) - digamma(th) + log(th) + 1 - log(th + mu) - (y + th) / (mu + th)))
nb_info <- function(th, mu, y, w) sum(w * (-trigamma(th + y) + trigamma(th) - 1 / th + 2 / (mu + th) - (y + th) / (mu + th)^2))
nb_loglik <- function(th, mu, y, w) sum(w * (lgamma(th + y) - lgamma(th) - lgamma(y + 1) + th * log(th) + y * log(mu + (y == 0)) - (th + y) * log(th + mu)))

# nb_glm is glm(negative.binomial(th, link)) refitted from start until its
# coefficients reach their fixed point, then twice more so the stored
# working weights are the converged ones (Fisher scoring off the
# canonical link converges linearly; glm keeps the weights of the
# iteration BEFORE its last update).
nb_glm <- function(th, link, d, start) {
  fam <- negative.binomial(th, link = link)
  m <- glm(y ~ x1 + x2, family = fam, data = d, weights = .f, control = nb_ctl, start = start)
  for (k in 1:500) {
    b <- coef(m)
    m <- glm(y ~ x1 + x2, family = fam, data = d, weights = .f, control = nb_ctl, start = b)
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-13) break
  }
  if (k == 500) stop("glm(negative.binomial) did not reach its fixed point")
  m <- glm(y ~ x1 + x2, family = fam, data = d, weights = .f, control = nb_ctl, start = coef(m))
  if (!m$converged) stop("glm did not converge")
  m
}

# nb_mle is the joint MLE glm.nb approximates: glm.nb, then the same
# alternation to its fixed point. Returns the final glm (negbin-classed,
# as glm.nb's object is: summary / vcov dispatch summary.negbin) and
# glm.nb's own fit.
nb_mle <- function(link, d, w = NULL) {
  d$.f <- if (is.null(w)) rep(1, nrow(d)) else w
  # glm.nb's initial poisson fit under the identity link finds no valid
  # coefficients from glm's default start; start it at least squares.
  start <- if (link == "identity") coef(lm(y ~ x1 + x2, data = d, weights = .f)) else NULL
  m0 <- do.call("glm.nb", list(formula = y ~ x1 + x2, data = d, weights = d$.f, link = as.name(link), start = start, control = glm.control(maxit = 100)))
  th <- m0$theta
  b <- coef(m0)
  for (k in 1:500) {
    m <- nb_glm(th, link, d, b)
    th1 <- theta.ml(m$y, fitted(m), sum(d$.f), d$.f, limit = 200, eps = 1e-12 * th)
    db <- max(abs(coef(m) - b) / pmax(abs(b), 1e-300))
    dt <- abs(as.vector(th1) - th) / th
    th <- as.vector(th1)
    b <- coef(m)
    if (db <= 1e-13 && dt <= 1e-13) break
  }
  if (k == 500) stop("the glm.nb alternation did not reach its fixed point")
  m <- nb_glm(th, link, d, b)
  if (abs(nb_score(th, fitted(m), m$y, d$.f)) > 1e-9 * abs(nb_info(th, fitted(m), m$y, d$.f))) stop("theta is not at its score root")
  m$theta <- th
  m$SE.theta <- 1 / sqrt(nb_info(th, fitted(m), m$y, d$.f))
  m$twologlik <- 2 * nb_loglik(th, fitted(m), m$y, d$.f)
  m$aic <- -m$twologlik + 2 * m$rank + 2
  class(m) <- c("negbin", class(m))
  # The cross-check: glm.nb's loose fit is the same estimator.
  nb_check("glm.nb vs its fixed point (coefficients)", coef(m0), coef(m), 1e-5)
  nb_check("glm.nb vs its fixed point (theta)", m0$theta, th, 1e-4)
  list(m = m, m0 = m0, d = d)
}

# nb_figures re-derives and returns the fit-level figures of m at theta th.
nb_figures <- function(name, m, th, estimated) {
  fam <- m$family
  X <- model.matrix(m)
  y <- m$y
  pw <- m$prior.weights
  eta <- m$linear.predictors
  mu <- fam$linkinv(eta)
  nb_check(paste(name, "fitted"), mu, fitted(m))
  me <- fam$mu.eta(eta)
  V <- mu + mu^2 / th
  nb_check(paste(name, "variance"), V, fam$variance(mu))
  W <- pw * me^2 / V
  nb_check(paste(name, "working weights"), W, weights(m, "working"))
  inv <- solve(t(X) %*% (W * X))
  vc <- if (estimated) vcov(m) else vcov(m, dispersion = 1)
  if (estimated && summary(m)$dispersion != 1) stop(name, ": summary.negbin dispersion is not 1")
  nb_check(paste(name, "vcov"), inv, vc)
  dr <- 2 * pw * (y * log(pmax(1, y) / mu) - (y + th) * log((y + th) / (mu + th)))
  nb_check(paste(name, "deviance"), sum(dr), deviance(m))
  wtdmu <- sum(pw * y) / sum(pw)
  nb_check(paste(name, "null deviance"), sum(fam$dev.resids(y, rep(wtdmu, length(y)), pw)), m$null.deviance)
  tll <- 2 * nb_loglik(th, mu, y, pw)
  nb_check(paste(name, "two log-likelihood"), tll, -fam$aic(y, rep(1, length(y)), mu, pw, deviance(m)))
  aic <- -tll + 2 * m$rank + if (estimated) 2 else 0
  if (estimated) {
    nb_check(paste(name, "aic"), aic, AIC(m))
    nb_check(paste(name, "theta SE"), 1 / sqrt(nb_info(th, mu, y, pw)), m$SE.theta)
  } else {
    nb_check(paste(name, "aic"), aic, AIC(m))
  }
  out <- list(
    coefficients = nb_g17(coef(m)), std_errors = nb_g17(sqrt(diag(vc))), vcov = nb_mat(vc),
    theta = nb_g17(th), two_log_lik = nb_g17(tll), aic = nb_g17(aic),
    deviance = nb_g17(deviance(m)), null_deviance = nb_g17(m$null.deviance),
    df_residual = nb_g17(m$df.residual))
  if (estimated) out$theta_std_err <- nb_g17(1 / sqrt(nb_info(th, mu, y, pw)))
  c(out, list(X = X, y = y, eta = eta, mu = mu, me = me, V = V, W = W, inv = inv))
}

# nb_rows is the per-row block of an unweighted case: residuals, hat
# values and the HC standard errors.
nb_rows <- function(name, m, f) {
  n <- length(f$y); p1 <- m$rank
  pres <- (f$y - f$mu) / sqrt(f$V)
  nb_check(paste(name, "pearson"), pres, residuals(m, type = "pearson"))
  th <- m$theta
  dres <- sign(f$y - f$mu) * sqrt(pmax(2 * (f$y * log(pmax(1, f$y) / f$mu) - (f$y + th) * log((f$y + th) / (f$mu + th))), 0))
  nb_check(paste(name, "deviance residual"), dres, residuals(m, type = "deviance"))
  wr <- (f$y - f$mu) / f$me
  nb_check(paste(name, "working residual"), wr, residuals(m, type = "working"))
  hat <- rowSums((f$X %*% f$inv) * f$X) * f$W
  nb_check(paste(name, "hat"), hat, hatvalues(m))
  sc <- (f$me * (f$y - f$mu) / f$V) * f$X
  hc <- function(type) {
    omega <- switch(type, HC0 = 1, HC1 = 1, HC3 = 1 / (1 - hat)^2)
    v <- f$inv %*% t(sc) %*% (omega * sc) %*% f$inv
    if (type == "HC1") v <- v * n / (n - p1)
    nb_check(paste(name, type), v, vcovHC(m, type = type))
    sqrt(diag(v))
  }
  list(hc0_std_errors = nb_g17(hc("HC0")), hc1_std_errors = nb_g17(hc("HC1")), hc3_std_errors = nb_g17(hc("HC3")),
    fitted = nb_g17(f$mu), pearson = nb_g17(pres), deviance_residuals = nb_g17(dres), working = nb_g17(wr), hat = nb_g17(hat),
    pres = pres, dres = dres, hatv = hat)
}

nb_cases <- list()
nb_keep <- c("coefficients", "std_errors", "vcov", "theta", "theta_std_err", "two_log_lik", "aic", "deviance", "null_deviance", "df_residual")

nb_case <- function(name, link, d, influence = FALSE) {
  r <- nb_mle(link, d)
  m <- r$m
  if (m$family$link != link) stop(name, ": wrong link")
  f <- nb_figures(name, m, m$theta, TRUE)
  rows <- nb_rows(name, m, f)
  out <- c(list(name = name, link = link, theta_fixed = FALSE, weighted = FALSE,
    rows = lapply(seq_len(nrow(d)), function(i) nb_g17(c(d$x1[i], d$x2[i], d$y[i])))),
    f[intersect(nb_keep, names(f))], rows[1:8])
  if (influence) {
    h <- rows$hatv; p1 <- m$rank
    std <- rows$pres / sqrt(1 - h)
    nb_check(paste(name, "rstandard"), std, rstandard(m))
    stdd <- rows$dres / sqrt(1 - h)
    nb_check(paste(name, "rstandard deviance"), stdd, rstandard(m, type = "deviance"))
    cook <- (rows$pres / (1 - h))^2 * h / p1
    nb_check(paste(name, "cooks"), cook, cooks.distance(m))
    m1 <- m
    m1$family$dispersion <- 1
    nb_check(paste(name, "rstudent (dispersion 1)"), std, rstudent(m1))
    dff <- sign(f$me) * rows$pres * sqrt(h) / (1 - h)
    nb_check(paste(name, "dffits (dispersion 1)"), dff, dffits(m1))
    out <- c(out, list(standardized = nb_g17(std), standardized_deviance = nb_g17(stdd),
      studentized = nb_g17(std), cooks = nb_g17(cook), dffits = nb_g17(dff)))
  }
  nb_cases[[length(nb_cases) + 1]] <<- out
}

# A fixed-theta case: glm(family = negative.binomial(theta)).
nb_case_fixed <- function(name, link, th, d) {
  d$.f <- rep(1, nrow(d))
  m <- nb_glm(th, link, d, NULL)
  m$theta <- th
  f <- nb_figures(name, m, th, FALSE)
  rows <- nb_rows(name, m, f)
  out <- c(list(name = name, link = link, theta_fixed = TRUE, weighted = FALSE,
    rows = lapply(seq_len(nrow(d)), function(i) nb_g17(c(d$x1[i], d$x2[i], d$y[i])))),
    f[intersect(nb_keep, names(f))], rows[1:8])
  nb_cases[[length(nb_cases) + 1]] <<- out
}

# A frequency-weighted case: glm.nb(weights = f) against the rep() expansion.
nb_case_freq <- function(name, link, d, fw) {
  rw <- nb_mle(link, d, fw)
  ex <- d[rep(seq_len(nrow(d)), fw), ]
  re <- nb_mle(link, ex)
  mw <- rw$m; me <- re$m
  nb_check(paste(name, "weighted vs expanded coefficients"), coef(mw), coef(me))
  nb_check(paste(name, "weighted vs expanded theta"), mw$theta, me$theta)
  nb_check(paste(name, "weighted vs expanded theta SE"), mw$SE.theta, me$SE.theta)
  nb_check(paste(name, "weighted vs expanded deviance"), deviance(mw), deviance(me))
  nb_check(paste(name, "weighted vs expanded null deviance"), mw$null.deviance, me$null.deviance)
  nb_check(paste(name, "weighted vs expanded log-likelihood"), mw$twologlik, me$twologlik)
  fe <- nb_figures(paste(name, "expanded"), me, me$theta, TRUE)
  eta <- drop(model.matrix(mw) %*% coef(me))
  mu <- mw$family$linkinv(eta)
  out <- c(list(name = name, link = link, theta_fixed = FALSE, weighted = TRUE,
    rows = lapply(seq_len(nrow(d)), function(i) nb_g17(c(d$x1[i], d$x2[i], d$y[i], fw[i])))),
    fe[intersect(nb_keep, names(fe))], list(fitted = nb_g17(mu)))
  nb_cases[[length(nb_cases) + 1]] <<- out
}

set.seed(20261105)
nb_design <- function(n) data.frame(x1 = round(runif(n, 0, 2), 3), x2 = round(runif(n, -1, 1), 3))

d <- nb_design(100)
d$y <- rnbinom(100, size = 2, mu = exp(0.6 + 0.5 * d$x1 - 0.4 * d$x2))
nb_case("nb_log", "log", d)

d <- nb_design(80)
d$y <- rnbinom(80, size = 3, mu = (1.5 + 0.5 * d$x1 - 0.3 * d$x2)^2)
nb_case("nb_sqrt", "sqrt", d)

d <- nb_design(80)
d$y <- rnbinom(80, size = 4, mu = 3 + 1.5 * d$x1 - 1 * d$x2)
nb_case("nb_identity", "identity", d)

d <- nb_design(70)
d$y <- rnbinom(70, size = 1.5, mu = exp(0.4 + 0.6 * d$x1 + 0.3 * d$x2))
nb_case_fixed("nb_log_theta_fixed", "log", 1.7, d)

d <- nb_design(40)
d$y <- rnbinom(40, size = 2.5, mu = exp(0.5 + 0.4 * d$x1 - 0.5 * d$x2))
nb_case_freq("nb_log_freq", "log", d, sample(1:4, 40, replace = TRUE))

# The influence case: one high-leverage row with a large count.
d <- nb_design(40)
d$x1[40] <- 4.5
d$y <- rnbinom(40, size = 2, mu = exp(0.3 + 0.5 * d$x1 + 0.4 * d$x2))
d$y[40] <- 3 * d$y[40] + 25
nb_case("nb_log_influence", "log", d, influence = TRUE)

# Fixed-theta cases with the dispersion ESTIMATED (Pulse dispersion
# "estimated"): R's summary(glm(family = negative.binomial(theta))), whose
# summary.glm estimates phi-hat = Pearson chi-square / df.residual (the
# family carries no dispersion slot, so stats:::estDisp is TRUE): vcov =
# phi-hat (X'WX)^-1, t p-values on df.residual, Wald-t intervals coef -/+
# qt(1 - a/2, df) se (confint.glm would profile), and the influence
# measures on the bare fit — rstandard / cooks.distance on phi-hat,
# rstudent / dffits on lm.influence's leave-one-out sigma,
# sqrt((phi-hat df - rP^2 / (1 - h)) / (df - 1)). The HC standard errors
# are dispersion-free (sandwich's bread.glm / estfun.glm fix it at 1 for
# a "Negative Binomial" family), so nb_rows pins them unchanged.
nb_disp_figures <- function(name, m, df) {
  inv <- summary(m)$cov.unscaled
  pres <- residuals(m, type = "pearson")
  phi <- sum(m$prior.weights * (m$y - fitted(m))^2 / m$family$variance(fitted(m))) / df
  vc <- phi * inv
  se <- sqrt(diag(vc))
  b <- coef(m)
  q <- qt(0.975, df)
  list(dispersion = phi, vcov = vc, std_errors = se, p_values = 2 * pt(-abs(b / se), df),
    ci_lower = b - q * se, ci_upper = b + q * se)
}

nb_case_fixed_disp <- function(name, link, th, d) {
  d$.f <- rep(1, nrow(d))
  m <- nb_glm(th, link, d, NULL)
  m$theta <- th
  f <- nb_figures(name, m, th, FALSE)
  rows <- nb_rows(name, m, f)
  s <- summary(m)
  df <- m$df.residual
  g <- nb_disp_figures(name, m, df)
  nb_check(paste(name, "summary dispersion"), g$dispersion, s$dispersion)
  nb_check(paste(name, "summary vcov"), g$vcov, vcov(m))
  nb_check(paste(name, "summary p-values"), g$p_values, s$coefficients[, 4])
  h <- rows$hatv; p1 <- m$rank
  std <- rows$pres / sqrt(g$dispersion * (1 - h))
  nb_check(paste(name, "rstandard"), std, rstandard(m))
  stdd <- rows$dres / sqrt(g$dispersion * (1 - h))
  nb_check(paste(name, "rstandard deviance"), stdd, rstandard(m, type = "deviance"))
  cook <- (rows$pres / (1 - h))^2 * h / (g$dispersion * p1)
  nb_check(paste(name, "cooks"), cook, cooks.distance(m))
  sig <- sqrt((g$dispersion * df - rows$pres^2 / (1 - h)) / (df - 1))
  stud <- rows$pres / (sig * sqrt(1 - h))
  nb_check(paste(name, "rstudent"), stud, rstudent(m))
  dff <- sign(f$me) * rows$pres * sqrt(h) / (sig * (1 - h))
  nb_check(paste(name, "dffits"), dff, dffits(m))
  f$vcov <- nb_mat(g$vcov)
  f$std_errors <- nb_g17(g$std_errors)
  out <- c(list(name = name, link = link, theta_fixed = TRUE, weighted = FALSE, dispersion_method = "estimated",
    rows = lapply(seq_len(nrow(d)), function(i) nb_g17(c(d$x1[i], d$x2[i], d$y[i])))),
    f[intersect(nb_keep, names(f))], rows[1:8],
    list(dispersion = nb_g17(g$dispersion), p_values = nb_g17(g$p_values), ci_lower = nb_g17(g$ci_lower), ci_upper = nb_g17(g$ci_upper),
      standardized = nb_g17(std), standardized_deviance = nb_g17(stdd), studentized = nb_g17(stud), cooks = nb_g17(cook), dffits = nb_g17(dff)))
  nb_cases[[length(nb_cases) + 1]] <<- out
}

# The frequency-weighted twin: glm(negative.binomial(theta), weights = f)
# against the rep() expansion, whose summary.glm reads df = sum(f) - p'
# (R's weighted glm would read nrow - p').
nb_case_fixed_disp_freq <- function(name, link, th, d, fw) {
  dw <- d; dw$.f <- fw
  mw <- nb_glm(th, link, dw, NULL)
  ex <- d[rep(seq_len(nrow(d)), fw), ]
  ex$.f <- rep(1, nrow(ex))
  me <- nb_glm(th, link, ex, NULL)
  me$theta <- th
  nb_check(paste(name, "weighted vs expanded coefficients"), coef(mw), coef(me))
  nb_check(paste(name, "weighted vs expanded deviance"), deviance(mw), deviance(me))
  fe <- nb_figures(paste(name, "expanded"), me, th, FALSE)
  df <- me$df.residual
  if (df != sum(fw) - me$rank) stop(name, ": expanded df")
  g <- nb_disp_figures(name, me, df)
  s <- summary(me)
  nb_check(paste(name, "summary dispersion"), g$dispersion, s$dispersion)
  nb_check(paste(name, "summary vcov"), g$vcov, vcov(me))
  nb_check(paste(name, "summary p-values"), g$p_values, s$coefficients[, 4])
  fe$vcov <- nb_mat(g$vcov)
  fe$std_errors <- nb_g17(g$std_errors)
  eta <- drop(model.matrix(mw) %*% coef(me))
  out <- c(list(name = name, link = link, theta_fixed = TRUE, weighted = TRUE, dispersion_method = "estimated",
    rows = lapply(seq_len(nrow(d)), function(i) nb_g17(c(d$x1[i], d$x2[i], d$y[i], fw[i])))),
    fe[intersect(nb_keep, names(fe))], list(fitted = nb_g17(mw$family$linkinv(eta)),
      dispersion = nb_g17(g$dispersion), p_values = nb_g17(g$p_values), ci_lower = nb_g17(g$ci_lower), ci_upper = nb_g17(g$ci_upper)))
  nb_cases[[length(nb_cases) + 1]] <<- out
}

d <- nb_design(70)
d$y <- rnbinom(70, size = 1.2, mu = exp(0.5 + 0.5 * d$x1 - 0.4 * d$x2))
nb_case_fixed_disp("nb_log_theta_fixed_disp", "log", 3, d)

d <- nb_design(40)
d$x1[40] <- 4.5
d$y <- rnbinom(40, size = 2, mu = (1.2 + 0.6 * d$x1 + 0.3 * d$x2)^2)
d$y[40] <- 2 * d$y[40] + 15
nb_case_fixed_disp("nb_sqrt_influence_theta_fixed_disp", "sqrt", 2.5, d)

d <- nb_design(40)
d$y <- rnbinom(40, size = 2, mu = exp(0.5 + 0.4 * d$x1 - 0.5 * d$x2))
nb_case_fixed_disp_freq("nb_log_theta_fixed_disp_freq", "log", 2, d, sample(1:4, 40, replace = TRUE))

write_golden("glm_nb", list(
  generator = "scripts/reference/gen_glm_nb.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
                  MASS = as.character(packageVersion("MASS")), sandwich = as.character(packageVersion("sandwich"))),
  note = paste(
    "y ~ x1 + x2, the glm.nb joint MLE (theta estimated) pushed to its fixed point, or glm(negative.binomial(theta)) when theta_fixed;",
    "terms (intercept), x1, x2. rows are [x1, x2, y] or [x1, x2, y, f]; per-row figures in row order.",
    "vcov / std_errors at dispersion 1 (summary.negbin). aic counts theta when it is estimated.",
    "_freq: glm.nb(weights = f), every fit-level figure the rep()-expanded fit's; fitted per distinct row.",
    "hc*_std_errors: sandwich::vcovHC (unweighted cases). influence case: rstudent / dffits at family$dispersion = 1.",
    "dispersion_method estimated: theta fixed, summary.glm's phi-hat = Pearson chi-square / df: vcov, std_errors, t p_values and",
    "Wald-t ci_lower / ci_upper (0.95) on it, influence on the bare fit (rstudent / dffits on the leave-one-out sigma); _freq against rep()."),
  cases = nb_cases
))
