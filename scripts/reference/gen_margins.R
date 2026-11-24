# gen_margins.R — R oracle for the regression marginal effects
# (RegressionSpec.Margins, U65 E3-S1, #627): average marginal effects
# (ame) and marginal effects at the mean (mem) on REG_OLS and REG_GLM,
# response and link scale, with delta-method standard errors on the fit's
# own coefficient covariance (classical, sandwich HC3, cluster); and
# REG_ORDINAL's per-level effects on each level's probability (U68 E4-S1,
# #652; the ordinal block below, written under ordinal_cases).
#
# Writes <out_dir>/margins.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_margins.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite, MASS 7.3-66 (glm.nb, theta.ml), sandwich 3.1-3
# (vcovHC, vcovCL) and, for the external cross-check only,
# marginaleffects 1.0.0 (with insight 1.7.0, data.table 1.18.6.1,
# generics 0.1.4 and Matrix 1.7-6 — Matrix pinned, the version
# marginaleffects resolves — installed from the Posit noble binary repo;
# versions recorded in the golden).
#
# The PRIMARY figures are analytic closed forms in base R, at the fit's
# fixed point (glm refitted from its own optimum until the coefficients
# stop moving, so the stored weights are the converged ones):
#   mu' = family$mu.eta(eta), mu'' the link's closed-form second
#   derivative (checked against a central difference of mu.eta),
#   w the prior weights (1 unweighted), x_i0 = 1;
#   ame  AME_j = b_j * sum(w mu'(eta_i)) / sum(w)
#        J_jk  = (j == k) * mbar + b_j * sum(w mu''(eta_i) x_ik) / sum(w)
#   mem  xbar = sum(w x_i) / sum(w), etabar = xbar' b
#        MEM_j = b_j mu'(etabar), J_jk = (j == k) mu'(etabar) + b_j mu''(etabar) xbar_k
#   link scale: the effect is b_j, J = e_j
#   binary = "contrast" (U69 #641): a term whose values are all 0 / 1
#   reports mu(x_j = 1) - mu(x_j = 0) instead (other terms as observed
#   under ame, at xbar under mem), J = sum(w (mu'(eta1) x1 - mu'(eta0)
#   x0)) / sum(w), checked against a central difference; on the link
#   scale and an identity link that is b_j again. marginaleffects reports
#   such a column as that contrast by default, so it cross-checks it.
#   se = sqrt(diag(J V J')), V = vcov(m) | sandwich::vcovHC(m, "HC3") |
#   sandwich::vcovCL(m, cluster = ~g, type = "HC1")
#   statistic = est / se; a Student t on df.residual(m) for lm and the
#   gamma glm under the classical vcov (their coefficient tables read
#   one), else a normal; p two-sided; ci = est -/+ q se at 0.95.
# The EXTERNAL cross-check is marginaleffects::avg_slopes (ame) and
# slopes(newdata = "mean") (mem) with the same vcov matrix, df and
# type; its slopes are finite differences and its Jacobian is numerical,
# so it agrees to about 1e-8 relative (measured max 2.3e-9 on the
# estimates, 6.8e-9 on the standard errors) — the Go test holds it at
# 1e-7 (TestReferenceOracle_MarginsMatchMarginaleffects), the analytic
# figures at 1e-9.
# The frequency-weighted case ("_freq") is glm(weights = f): its figures
# are the rep()-expanded fit's (the script asserts the weighted ame / mem
# and vcov equal the expansion's) and marginaleffects reads it with
# wts = "f" (ame) and at the weighted means (mem: slopes(newdata = "mean")
# ignores weights, so the point is passed explicitly).
#
# Seeded (set.seed(20261106)); draws its own rows after every other
# golden, so no earlier golden moves. Rows are [x1, x2, y], plus g
# (cluster) or f (frequency weight) where the case names one.

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_margins.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("MASS")) != "7.3.66") stop("gen_margins.R pins MASS 7.3-66, got ", packageVersion("MASS"))
if (as.character(packageVersion("sandwich")) != "3.1.3") stop("gen_margins.R pins sandwich 3.1-3, got ", packageVersion("sandwich"))
mg_have_me <- requireNamespace("marginaleffects", quietly = TRUE)
if (mg_have_me && as.character(packageVersion("marginaleffects")) != "1.0.0") stop("gen_margins.R pins marginaleffects 1.0.0, got ", packageVersion("marginaleffects"))
# The Matrix marginaleffects resolves (its import, from the user library):
# pinned like the others and recorded in the golden. gen_multivariate.R
# pins its own Matrix (1.7-5), so the two regenerate under separate
# library setups (docs/src/internals/regenerating-goldens.md, The R
# toolchain).
if (mg_have_me && as.character(packageVersion("Matrix")) != "1.7.6") stop("gen_margins.R pins Matrix 1.7-6 (marginaleffects' import), got ", packageVersion("Matrix"))
if (!mg_have_me) message("gen_margins.R: marginaleffects not installed; the external cross-check is skipped")

mg_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
mg_check <- function(what, got, want, tol = 1e-10) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  if (max(abs(got - want) / pmax(1, abs(want))) > tol) stop(what, ": closed form disagrees with R (", max(abs(got - want) / pmax(1, abs(want))), ")")
}

mg_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# mg_d2 is the link's d^2 mu / d eta^2 in closed form.
mg_d2 <- function(link, eta) {
  switch(link,
    identity = rep(0, length(eta)),
    log = exp(eta),
    sqrt = rep(2, length(eta)),
    inverse = 2 / eta^3,
    logit = { mu <- plogis(eta); mu * (1 - mu) * (1 - 2 * mu) },
    probit = -eta * dnorm(eta),
    cloglog = { e <- exp(eta); e * exp(-e) * (1 - e) },
    stop("no second derivative for link ", link))
}

# mg_refit is glm refitted from its own optimum until the coefficients
# stop moving (then twice more, so the stored weights are converged).
mg_refit <- function(formula, family, d, w) {
  d$.w <- w
  m <- glm(formula, family = family, data = d, weights = .w, control = mg_ctl)
  for (k in 1:500) {
    b <- coef(m)
    m <- glm(formula, family = family, data = d, weights = .w, control = mg_ctl, start = b)
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-14) break
  }
  if (k == 500) stop("glm did not reach its fixed point")
  m <- glm(formula, family = family, data = d, weights = .w, control = mg_ctl, start = coef(m))
  m <- glm(formula, family = family, data = d, weights = .w, control = mg_ctl, start = coef(m))
  m
}

# mg_nb is the negative binomial joint MLE (glm.nb pushed to its fixed
# point with MASS's own pieces, as gen_glm_nb.R does).
mg_nb <- function(d, w) {
  d$.w <- w
  m0 <- glm.nb(y ~ x1 + x2, data = d, weights = .w, control = glm.control(maxit = 100))
  th <- m0$theta
  b <- coef(m0)
  for (k in 1:500) {
    m <- glm(y ~ x1 + x2, family = negative.binomial(th), data = d, weights = .w, control = mg_ctl, start = b)
    for (r in 1:200) {
      b1 <- coef(m)
      m <- glm(y ~ x1 + x2, family = negative.binomial(th), data = d, weights = .w, control = mg_ctl, start = b1)
      if (max(abs(coef(m) - b1) / pmax(abs(b1), 1e-300)) <= 1e-14) break
    }
    th1 <- as.vector(theta.ml(m$y, fitted(m), sum(d$.w), d$.w, limit = 200, eps = 1e-12 * th))
    db <- max(abs(coef(m) - b) / pmax(abs(b), 1e-300))
    dt <- abs(th1 - th) / th
    th <- th1
    b <- coef(m)
    if (db <= 1e-13 && dt <= 1e-13) break
  }
  if (k == 500) stop("the glm.nb alternation did not reach its fixed point")
  m <- glm(y ~ x1 + x2, family = negative.binomial(th), data = d, weights = .w, control = mg_ctl, start = b)
  m <- glm(y ~ x1 + x2, family = negative.binomial(th), data = d, weights = .w, control = mg_ctl, start = coef(m))
  m$theta <- th
  m
}

# mg_effects is the analytic block of fit m: type ame | mem, scale
# response | link, V the coefficient covariance, df the t df (NULL = z).
mg_isbin <- function(v) all(v %in% c(0, 1))

# mg_contrast is the response-scale contrast of column j at the points
# pts (rows, intercept first) with weights ww, and its gradient in b.
mg_contrast <- function(fam, b, pts, ww, j) {
  p1 <- pts; p1[, j] <- 1
  p0 <- pts; p0[, j] <- 0
  e1 <- drop(p1 %*% b); e0 <- drop(p0 %*% b)
  list(est = sum(ww * (fam$linkinv(e1) - fam$linkinv(e0))) / sum(ww),
       J = colSums(ww * (fam$mu.eta(e1) * p1 - fam$mu.eta(e0) * p0)) / sum(ww))
}

mg_effects <- function(m, link, type, scale, V, df, w, binary = "slope") {
  X <- model.matrix(m)
  b <- coef(m)
  k <- length(b)
  eta <- drop(X %*% b)
  fam <- m$family
  if (!is.null(m$linear.predictors)) mg_check("eta", eta, m$linear.predictors)
  # The closed-form mu'' against a central difference of mu.eta.
  if (!inherits(m, "lm") || inherits(m, "glm")) {
    h <- 1e-5 * pmax(1, abs(eta))
    num <- (fam$mu.eta(eta + h) - fam$mu.eta(eta - h)) / (2 * h)
    mg_check("mu''", mg_d2(link, eta), num, 1e-6)
  }
  sw <- sum(w)
  xbar <- colSums(w * X) / sw
  terms <- c("x1", "x2")
  J <- matrix(0, length(terms), k, dimnames = list(terms, colnames(X)))
  est <- setNames(numeric(length(terms)), terms)
  for (t in terms) {
    j <- match(t, colnames(X))
    if (scale == "link" || link == "identity") {
      est[t] <- b[j]
      J[t, j] <- 1
    } else if (binary == "contrast" && mg_isbin(X[, j])) {
      pts <- if (type == "mem") matrix(xbar, nrow = 1) else X
      ww <- if (type == "mem") 1 else w
      cc <- mg_contrast(fam, b, pts, ww, j)
      h <- 1e-6
      num <- sapply(seq_len(k), function(a) {
        bp <- b; bm <- b; bp[a] <- bp[a] + h; bm[a] <- bm[a] - h
        (mg_contrast(fam, bp, pts, ww, j)$est - mg_contrast(fam, bm, pts, ww, j)$est) / (2 * h)
      })
      mg_check("contrast gradient", cc$J, num, 1e-7)
      est[t] <- cc$est
      J[t, ] <- cc$J
    } else if (type == "ame") {
      mbar <- sum(w * fam$mu.eta(eta)) / sw
      g <- colSums((w * mg_d2(link, eta)) * X) / sw
      est[t] <- b[j] * mbar
      J[t, ] <- b[j] * g
      J[t, j] <- J[t, j] + mbar
    } else {
      etabar <- sum(xbar * b)
      est[t] <- b[j] * fam$mu.eta(etabar)
      J[t, ] <- b[j] * mg_d2(link, etabar) * xbar
      J[t, j] <- J[t, j] + fam$mu.eta(etabar)
    }
  }
  se <- sqrt(diag(J %*% V %*% t(J)))
  stat <- est / se
  if (is.null(df)) {
    p <- 2 * pnorm(-abs(stat))
    q <- qnorm(0.975)
  } else {
    p <- 2 * pt(-abs(stat), df)
    q <- qt(0.975, df)
  }
  out <- list(estimates = mg_g17(est), std_errors = mg_g17(se), statistics = mg_g17(stat), p_values = mg_g17(p),
    ci_lower = mg_g17(est - q * se), ci_upper = mg_g17(est + q * se))
  if (!is.null(df)) out$df <- mg_g17(df)
  if (type == "mem") out$at <- mg_g17(xbar[c("x1", "x2")])
  if (binary == "contrast") {
    out$binary <- binary
    out$contrasts <- I(terms[sapply(terms, function(t) mg_isbin(X[, t]))])
  }
  list(json = out, est = est, se = se)
}

# mg_me is the marginaleffects cross-check (estimates and std errors) of
# fit m over its data nd: avg_slopes over nd's rows (weighted by wts), or
# slopes at the (weighted) means of nd. nd is passed explicitly —
# insight::get_data would re-evaluate the formula's data name in the
# caller's frame.
mg_me <- function(m, type, scale, V, df, nd, w, wts = NULL) {
  if (!mg_have_me) return(NULL)
  dfa <- if (is.null(df)) Inf else df
  args <- list(m, variables = c("x1", "x2"), vcov = V, type = scale, df = dfa)
  if (type == "ame") {
    args$newdata <- nd
    if (!is.null(wts)) args$wts <- wts
    s <- do.call(marginaleffects::avg_slopes, args)
  } else {
    args$newdata <- data.frame(x1 = sum(w * nd$x1) / sum(w), x2 = sum(w * nd$x2) / sum(w))
    s <- do.call(marginaleffects::slopes, args)
  }
  s <- s[match(c("x1", "x2"), s$term), ]
  list(me_estimates = mg_g17(s$estimate), me_std_errors = mg_g17(s$std.error))
}

mg_cases <- list()

# mg_case fits one case and records the analytic block (+ the
# marginaleffects one). fit is a function of the data frame and weights.
mg_case <- function(name, model, family, link, d, type = "ame", scale = "response", covariance = "classical",
                    weighted = FALSE, cluster = FALSE, binary = "slope") {
  w <- if (weighted) d$f else rep(1, nrow(d))
  m <- switch(model,
    lm = lm(y ~ x1 + x2, data = d),
    nb = mg_nb(d, w),
    mg_refit(y ~ x1 + x2, family, d, w))
  V <- switch(covariance,
    # The negative binomial at theta-hat reads dispersion 1
    # (summary.negbin); a bare glm(negative.binomial(theta)) would
    # estimate one.
    classical = if (model == "nb") vcov(m, dispersion = 1) else vcov(m),
    hc3 = vcovHC(m, type = "HC3"),
    cluster = vcovCL(m, cluster = ~g, type = "HC1"))
  tdist <- model == "lm" || (identical(family$family, "Gamma") && covariance == "classical")
  df <- if (tdist) df.residual(m) else NULL
  eff <- mg_effects(m, link, type, scale, V, df, w, binary)
  rows <- lapply(seq_len(nrow(d)), function(i) {
    r <- c(d$x1[i], d$x2[i], d$y[i])
    if (cluster) r <- c(r, d$g[i])
    if (weighted) r <- c(r, d$f[i])
    mg_g17(r)
  })
  out <- c(list(name = name, model = model, family = if (model == "lm") "" else if (model == "nb") "negative_binomial" else tolower(family$family),
    link = link, type = type, scale = scale, covariance = covariance, weighted = weighted, cluster = cluster,
    rows = rows, coefficients = mg_g17(coef(m)), vcov = lapply(seq_len(nrow(V)), function(i) mg_g17(V[i, ]))), eff$json)
  if (weighted) {
    # The rep() expansion: same effects, same vcov.
    ex <- d[rep(seq_len(nrow(d)), d$f), ]
    me <- if (model == "nb") mg_nb(ex, rep(1, nrow(ex))) else mg_refit(y ~ x1 + x2, family, ex, rep(1, nrow(ex)))
    Ve <- vcov(me)
    mg_check(paste(name, "weighted vs expanded vcov"), V, Ve)
    effe <- mg_effects(me, link, type, scale, Ve, df, rep(1, nrow(ex)), binary)
    mg_check(paste(name, "weighted vs expanded estimates"), eff$est, effe$est)
    mg_check(paste(name, "weighted vs expanded std errors"), eff$se, effe$se)
    cx <- mg_me(m, type, scale, V, df, d, w, wts = if (type == "ame") "f" else NULL)
  } else {
    cx <- mg_me(m, type, scale, V, df, d, w)
  }
  if (!is.null(cx)) out <- c(out, cx)
  mg_cases[[length(mg_cases) + 1]] <<- out
}

set.seed(20261106)
mg_design <- function(n) data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(runif(n, -1, 1), 3))

d <- mg_design(100)
d$y <- 1 + 0.8 * d$x1 - 0.5 * d$x2 + rnorm(100, sd = 0.7)
mg_case("ols_ame", "lm", NULL, "identity", d)
mg_case("ols_mem", "lm", NULL, "identity", d, type = "mem")

d <- mg_design(150)
d$y <- rbinom(150, 1, plogis(-0.3 + 0.9 * d$x1 - 0.7 * d$x2))
mg_case("logit_ame", "glm", binomial("logit"), "logit", d)
mg_case("logit_mem", "glm", binomial("logit"), "logit", d, type = "mem")
mg_case("logit_link", "glm", binomial("logit"), "logit", d, scale = "link")
mg_case("logit_hc3", "glm", binomial("logit"), "logit", d, covariance = "hc3")
mg_case("probit_ame", "glm", binomial("probit"), "probit", d)
mg_case("cloglog_ame", "glm", binomial("cloglog"), "cloglog", d)

d <- mg_design(120)
d$y <- rpois(120, exp(0.4 + 0.5 * d$x1 - 0.6 * d$x2))
d$g <- rep(1:12, each = 10)
mg_case("poisson_ame", "glm", poisson("log"), "log", d)
mg_case("poisson_mem", "glm", poisson("log"), "log", d, type = "mem")
mg_case("poisson_link", "glm", poisson("log"), "log", d, scale = "link")
mg_case("poisson_cluster", "glm", poisson("log"), "log", d, covariance = "cluster", cluster = TRUE)

d <- mg_design(100)
d$y <- rgamma(100, shape = 4, rate = 4 * (0.8 + 0.2 * d$x1 + 0.15 * d$x2))
mg_case("gamma_ame", "glm", Gamma("inverse"), "inverse", d)
mg_case("gamma_log_mem", "glm", Gamma("log"), "log", d, type = "mem")

d <- mg_design(100)
d$y <- rnbinom(100, size = 2, mu = exp(0.6 + 0.5 * d$x1 - 0.4 * d$x2))
mg_case("nb_ame", "nb", NULL, "log", d)

d <- mg_design(50)
d$y <- rbinom(50, 1, plogis(0.2 + 1.1 * d$x1 - 0.8 * d$x2))
d$f <- sample(1:4, 50, replace = TRUE)
mg_case("logit_ame_freq", "glm", binomial("logit"), "logit", d, weighted = TRUE)
mg_case("logit_mem_freq", "glm", binomial("logit"), "logit", d, type = "mem", weighted = TRUE)

# Derived predictors, differentiated as given (Pulse margins over a
# feature output): x1 is a pre-computed log1p(z) (Pulse FEAT_LOG's
# formula) and x2 a 0/1 dummy of a two-level category g (FEAT_ONE_HOT's
# "b" column). x1's effect is the slope in the column itself; under
# binary = "contrast" (U69 #641) the dummy x2 reports its discrete
# 1 - 0 contrast, so the fit and its effects are an ordinary logit on
# (x1, x2); the case also carries the raw z and g ("raw": [z, g index],
# g "a" = 0, "b" = 1) so the root test builds the same columns through
# the features.
d <- data.frame(z = round(runif(120, 0, 6), 3), g = sample(0:1, 120, replace = TRUE))
d$x1 <- log1p(d$z)
d$x2 <- as.numeric(d$g == 1)
d$y <- rbinom(120, 1, plogis(-0.8 + 0.7 * d$x1 - 0.9 * d$x2))
mg_case("logit_ame_derived", "glm", binomial("logit"), "logit", d, binary = "contrast")
mg_cases[[length(mg_cases)]]$raw <- lapply(seq_len(nrow(d)), function(i) mg_g17(c(d$z[i], d$g[i])))

# ---- REG_ORDINAL (U68 E4-S1, #652) -----------------------------------
# Per-level marginal effects of a proportional-odds fit (MASS::polr):
# P_k = F(zeta_k - eta) - F(zeta_{k-1} - eta), eta = x'beta (zeta_0 =
# -Inf, zeta_K = +Inf), so dP_k/dx_j = -beta_j D_k, D_k = f(zeta_k - eta)
# - f(zeta_{k-1} - eta). The PRIMARY figures are closed forms at the MLE
# (polr's BFGS optimum polished by Newton on the analytic gradient and
# Hessian, gen_ordinal.R's method, written out again here so this script
# stays standalone): ame the prior-weighted mean over the rows, mem at the
# weighted means; the delta-method Jacobian in theta = c(beta, zeta)
#   d/dbeta_m     = -(m == j) D_k + beta_j (f'(zeta_k - eta) - f'(zeta_{k-1} - eta)) x_m
#   d/dzeta_k     = -beta_j f'(zeta_k - eta)      (k < K)
#   d/dzeta_{k-1} = +beta_j f'(zeta_{k-1} - eta)  (k > 1)
# averaged the same way; se = sqrt(diag(J V J')), V the analytic inverse
# information, or its hc1 / cluster (CR1) sandwich (gen_ordinal.R's
# formulas). scale link is the effect on each cumulative link
# g(P(Y <= k)) = zeta_k - eta: -beta_j for k < K, J = -e_j. Statistic,
# two-sided normal p and interval at 0.95 (Wald z: the coefficient
# table's). The K effects of a term sum to 0 (asserted). The EXTERNAL
# cross-check is marginaleffects avg_slopes / slopes(newdata = the
# weighted means) on the polr fit with its coefficients set to the
# polished MLE (so only the numerical derivatives differ) and the same V;
# the link scale has no marginaleffects counterpart for polr (no
# me_ figures). Its Jacobian is Richardson-extrapolated (see mo_case),
# agreeing to about 1e-8. The frequency-weighted cases are polr(weights = f),
# asserted equal to the rep()-expanded fit. Effects are outcome-major:
# for each level, for each term. Rows are [x1, x2, y, w, g] (y 1-based,
# w = 1 unweighted, g the cluster id). Seeded apart
# (set.seed(20261117)), after every case above, so none of them moves.

mo_links <- list(
  logit = list(method = "logistic", F = plogis, f = dlogis,
               fp = function(x) dlogis(x) * (1 - 2 * plogis(x))),
  probit = list(method = "probit", F = pnorm, f = dnorm, fp = function(x) -x * dnorm(x)),
  cloglog = list(method = "cloglog", F = function(x) -expm1(-exp(x)), f = function(x) exp(x - exp(x)),
                 fp = function(x) exp(x - exp(x)) * (1 - exp(x))))

# mo_parts: per-row scores, gradient and Hessian of the weighted
# log-likelihood at theta = c(beta, zeta).
mo_parts <- function(theta, X, y, w, J, L) {
  p <- ncol(X)
  beta <- theta[seq_len(p)]
  zeta <- theta[p + seq_len(J - 1)]
  eta <- drop(X %*% beta)
  up <- y < J; lo <- y > 1
  a <- ifelse(up, c(zeta, 0)[y] - eta, 0)
  b <- ifelse(lo, c(0, zeta)[y] - eta, 0)
  Fa <- ifelse(up, L$F(a), 1); Fb <- ifelse(lo, L$F(b), 0)
  fa <- ifelse(up, L$f(a), 0); fb <- ifelse(lo, L$f(b), 0)
  fpa <- ifelse(up, L$fp(a), 0); fpb <- ifelse(lo, L$fp(b), 0)
  pr <- Fa - Fb
  q <- p + J - 1
  S <- matrix(0, length(y), q)
  H <- matrix(0, q, q)
  g <- (fa - fb) / pr
  for (i in seq_along(y)) {
    s <- numeric(q)
    s[seq_len(p)] <- -g[i] * X[i, ]
    h <- matrix(0, q, q)
    h[seq_len(p), seq_len(p)] <- ((fpa[i] - fpb[i]) / pr[i] - g[i]^2) * tcrossprod(X[i, ])
    if (up[i]) {
      k <- p + y[i]
      s[k] <- fa[i] / pr[i]
      h[k, k] <- fpa[i] / pr[i] - (fa[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (-fpa[i] / pr[i] + fa[i] * g[i] / pr[i])
    }
    if (lo[i]) {
      k <- p + y[i] - 1
      s[k] <- -fb[i] / pr[i]
      h[k, k] <- -fpb[i] / pr[i] - (fb[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (fpb[i] / pr[i] - fb[i] * g[i] / pr[i])
    }
    if (up[i] && lo[i]) {
      ku <- p + y[i]; kl <- ku - 1
      h[ku, kl] <- h[kl, ku] <- fa[i] * fb[i] / pr[i]^2
    }
    S[i, ] <- s
    H <- H + w[i] * h
  }
  list(S = S, grad = colSums(w * S), H = H)
}

mo_newton <- function(theta, X, y, w, J, L) {
  for (it in 1:100) {
    pt <- mo_parts(theta, X, y, w, J, L)
    step <- solve(pt$H, pt$grad)
    theta <- theta - step
    if (max(abs(step) / pmax(abs(theta), 1)) < 1e-15) break
  }
  if (it == 100) stop("ordinal Newton polish did not converge")
  theta
}

# mo_effects: the closed-form block (estimates and standard errors,
# outcome-major) of theta / V over rows X with prior weights w.
# mo_contrast: the per-level contrast P_k(x_j = 1) - P_k(x_j = 0) at
# the points pts with weights ww (a function of theta alone).
mo_contrast <- function(theta, pts, ww, J, L, j) {
  p <- ncol(pts)
  beta <- theta[seq_len(p)]
  zeta <- theta[p + seq_len(J - 1)]
  pk <- function(pp, k) {
    eta <- drop(pp %*% beta)
    (if (k < J) L$F(zeta[k] - eta) else 1) - (if (k > 1) L$F(zeta[k - 1] - eta) else 0)
  }
  p1 <- pts; p1[, j] <- 1
  p0 <- pts; p0[, j] <- 0
  sapply(seq_len(J), function(k) sum(ww * (pk(p1, k) - pk(p0, k))) / sum(ww))
}

mo_effects <- function(theta, V, X, w, J, L, type, scale, binary = "slope") {
  p <- ncol(X)
  beta <- theta[seq_len(p)]
  zeta <- theta[p + seq_len(J - 1)]
  terms <- colnames(X)
  q <- length(theta)
  pts <- if (type == "mem") matrix(colSums(w * X) / sum(w), nrow = 1) else X
  ww <- if (type == "mem") 1 else w
  outcomes <- if (scale == "link") seq_len(J - 1) else seq_len(J)
  est <- c(); se <- c(); jacs <- list()
  for (k in outcomes) {
    for (j in seq_len(p)) {
      Jv <- numeric(q)
      if (scale == "link") {
        e <- -beta[j]
        Jv[j] <- -1
      } else if (binary == "contrast" && mg_isbin(X[, j])) {
        # The closed-form gradient of the level-k contrast.
        p1 <- pts; p1[, j] <- 1
        p0 <- pts; p0[, j] <- 0
        side <- function(pp) {
          eta <- drop(pp %*% beta)
          fu <- if (k < J) L$f(zeta[k] - eta) else 0
          fl <- if (k > 1) L$f(zeta[k - 1] - eta) else 0
          list(D = fu - fl, fu = fu, fl = fl)
        }
        s1 <- side(p1); s0 <- side(p0)
        e <- mo_contrast(theta, pts, ww, J, L, j)[k]
        Jv[seq_len(p)] <- -colSums(ww * (s1$D * p1 - s0$D * p0)) / sum(ww)
        if (k < J) Jv[p + k] <- sum(ww * (s1$fu - s0$fu)) / sum(ww)
        if (k > 1) Jv[p + k - 1] <- -sum(ww * (s1$fl - s0$fl)) / sum(ww)
        h <- 1e-6
        num <- sapply(seq_len(q), function(a) {
          tp <- theta; tm <- theta; tp[a] <- tp[a] + h; tm[a] <- tm[a] - h
          (mo_contrast(tp, pts, ww, J, L, j)[k] - mo_contrast(tm, pts, ww, J, L, j)[k]) / (2 * h)
        })
        mg_check("ordinal contrast gradient", Jv, num, 1e-7)
      } else {
        eta <- drop(pts %*% beta)
        fu <- if (k < J) L$f(zeta[k] - eta) else 0
        fl <- if (k > 1) L$f(zeta[k - 1] - eta) else 0
        fpu <- if (k < J) L$fp(zeta[k] - eta) else 0
        fpl <- if (k > 1) L$fp(zeta[k - 1] - eta) else 0
        D <- sum(ww * (fu - fl)) / sum(ww)
        e <- -beta[j] * D
        Jv[seq_len(p)] <- beta[j] * colSums(ww * (fpu - fpl) * pts) / sum(ww)
        Jv[j] <- Jv[j] - D
        if (k < J) Jv[p + k] <- -beta[j] * sum(ww * fpu) / sum(ww)
        if (k > 1) Jv[p + k - 1] <- beta[j] * sum(ww * fpl) / sum(ww)
      }
      est <- c(est, e)
      se <- c(se, sqrt(drop(t(Jv) %*% V %*% Jv)))
    }
  }
  if (scale == "response") {
    sums <- colSums(matrix(est, nrow = J, byrow = TRUE))
    if (max(abs(sums)) > 1e-12) stop("ordinal effects of a term do not sum to 0")
  }
  list(est = est, se = se, outcomes = outcomes)
}

mo_cases <- list()

# mo_case fits one ordinal case: d has x1, x2, y (1..J), w and g.
mo_case <- function(name, d, link = "logit", type = "ame", scale = "response", covariance = "classical", weighted = FALSE,
                    binary = "slope") {
  L <- mo_links[[link]]
  if (!weighted) d$w <- 1
  J <- max(d$y)
  if (!all(sort(unique(d$y)) == seq_len(J))) stop(name, ": empty level")
  X <- as.matrix(d[, c("x1", "x2")])
  w <- d$w
  dd <- data.frame(yf = factor(d$y, levels = seq_len(J)), x1 = d$x1, x2 = d$x2, .w = w)
  pf <- suppressWarnings(polr(yf ~ x1 + x2, data = dd, weights = .w, method = L$method, Hess = TRUE,
                              control = list(reltol = 1e-14, maxit = 5000)))
  theta <- mo_newton(c(coef(pf), pf$zeta), X, d$y, w, J, L)
  mg_check(paste(name, "polr vs polished MLE"), c(coef(pf), pf$zeta), theta, 1e-6)
  pt <- mo_parts(theta, X, d$y, w, J, L)
  if (max(abs(pt$grad)) > 1e-9) stop(name, ": gradient not zero at the polished point")
  Hinv <- solve(-pt$H)
  q <- length(theta)
  nstar <- sum(w)
  V <- switch(covariance,
    classical = Hinv,
    hc1 = Hinv %*% crossprod(sqrt(w) * pt$S) %*% Hinv * nstar / (nstar - q),
    cluster = {
      Sg <- rowsum(w * pt$S, d$g)
      G <- nrow(Sg)
      Hinv %*% crossprod(Sg) %*% Hinv * G / (G - 1) * (nstar - 1) / (nstar - q)
    })
  if (!weighted && covariance == "hc1") mg_check(paste(name, "sandwich(polr)"), sandwich(pf, adjust = TRUE), V, 1e-4)
  if (!weighted && covariance == "cluster") mg_check(paste(name, "vcovCL(polr)"), vcovCL(pf, cluster = d$g, type = "HC1"), V, 1e-4)
  eff <- mo_effects(theta, V, X, w, J, L, type, scale, binary)
  stat <- eff$est / eff$se
  zq <- qnorm(0.975)
  out <- list(name = name, link = link, type = type, scale = scale, covariance = covariance, weighted = weighted,
    levels = as.character(seq_len(J)), outcomes = as.character(eff$outcomes), terms = c("x1", "x2"),
    rows = lapply(seq_len(nrow(d)), function(i) mg_g17(c(d$x1[i], d$x2[i], d$y[i], d$w[i], d$g[i]))),
    coefficients = mg_g17(theta), vcov = lapply(seq_len(q), function(i) mg_g17(V[i, ])),
    estimates = mg_g17(eff$est), std_errors = mg_g17(eff$se), statistics = mg_g17(stat),
    p_values = mg_g17(2 * pnorm(-abs(stat))), ci_lower = mg_g17(eff$est - zq * eff$se), ci_upper = mg_g17(eff$est + zq * eff$se))
  if (type == "mem") out$at <- mg_g17(colSums(w * X) / sum(w))
  if (binary == "contrast") {
    out$binary <- binary
    out$contrasts <- I(c("x1", "x2")[c(mg_isbin(X[, 1]), mg_isbin(X[, 2]))])
  }
  if (weighted) {
    ex <- d[rep(seq_len(nrow(d)), d$w), ]
    Xe <- as.matrix(ex[, c("x1", "x2")])
    te <- mo_newton(theta, Xe, ex$y, rep(1, nrow(ex)), J, L)
    Ve <- solve(-mo_parts(te, Xe, ex$y, rep(1, nrow(ex)), J, L)$H)
    mg_check(paste(name, "weighted vs expanded vcov"), V, Ve)
    effe <- mo_effects(te, Ve, Xe, rep(1, nrow(ex)), J, L, type, scale, binary)
    mg_check(paste(name, "weighted vs expanded estimates"), eff$est, effe$est)
    mg_check(paste(name, "weighted vs expanded std errors"), eff$se, effe$se)
  }
  if (mg_have_me && scale == "response") {
    # The fit at the polished MLE: marginaleffects predicts through
    # polr's coefficients and zeta, so only its numerical derivatives
    # differ from the closed forms.
    pf$coefficients[] <- theta[1:2]
    pf$zeta[] <- theta[-(1:2)]
    Vn <- V
    dimnames(Vn) <- dimnames(vcov(pf))
    # Its default forward-difference Jacobian of a finite-difference
    # slope agrees to only ~1e-3 on polr; Richardson extrapolation (d =
    # 0.03, r = 4) over a 1e-4 slope step brings it to ~1e-8 (measured;
    # the closed forms themselves match a central-difference Jacobian).
    old_nd <- options(marginaleffects_numDeriv = list(method = "Richardson", method.args = list(eps = 1e-4, d = 0.03, r = 4)))
    on.exit(options(old_nd), add = TRUE)
    args <- list(pf, variables = c("x1", "x2"), vcov = Vn, eps = 1e-4)
    if (type == "ame") {
      args$newdata <- dd
      if (weighted) args$wts <- ".w"
      s <- do.call(marginaleffects::avg_slopes, args)
    } else {
      args$newdata <- data.frame(x1 = sum(w * d$x1) / sum(w), x2 = sum(w * d$x2) / sum(w), .w = 1)
      s <- do.call(marginaleffects::slopes, args)
    }
    key <- paste(s$group, s$term)
    want <- paste(rep(as.character(seq_len(J)), each = 2), rep(c("x1", "x2"), J))
    s <- s[match(want, key), ]
    if (anyNA(s$estimate)) stop(name, ": marginaleffects did not report every level and term")
    out$me_estimates <- mg_g17(s$estimate)
    out$me_std_errors <- mg_g17(s$std.error)
  }
  mo_cases[[length(mo_cases) + 1]] <<- out
}

set.seed(20261117)
mo_design <- function(n, J, b, g = 10) {
  d <- data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(runif(n, -1, 1), 3))
  eta <- b[1] * d$x1 + b[2] * d$x2
  cuts <- qlogis(seq_len(J - 1) / J)
  d$y <- 1 + rowSums(outer(eta + rlogis(n), cuts, ">"))
  d$g <- rep(seq_len(g), length.out = n)
  d$w <- 1
  d
}
d <- mo_design(160, 4, c(0.9, -0.7))
mo_case("ord_logit_ame", d)
mo_case("ord_logit_mem", d, type = "mem")
mo_case("ord_logit_link", d, scale = "link")
mo_case("ord_logit_mem_link", d, type = "mem", scale = "link")
mo_case("ord_probit_ame", d, link = "probit")
mo_case("ord_cloglog_mem", d, link = "cloglog", type = "mem")
mo_case("ord_logit_hc1", d, covariance = "hc1")
mo_case("ord_logit_cluster", d, covariance = "cluster")
d <- mo_design(70, 3, c(1.1, -0.8), g = 7)
d$w <- sample(1:3, 70, replace = TRUE)
mo_case("ord_logit_ame_freq", d, weighted = TRUE)
mo_case("ord_logit_mem_freq", d, type = "mem", weighted = TRUE)

# --- REG_MULTINOMIAL (U68 E4-S2, #659) ---------------------------------
# The baseline-category logit's per-category effects, closed forms at the
# multinom MLE Newton-polished as gen_multinomial.R does. Per row i, with
# pi_ic the category probabilities, b_c the category's coefficients (b_r
# = 0 for the reference) and bbar_ij = sum_m pi_im b_mj:
#   response  d pi_c / d x_j = pi_c (b_cj - bbar_j); ame the weighted mean
#             over the rows, mem at the weighted means; one effect per
#             category (they sum to 0 over the categories, asserted);
#   link      d eta_c / d x_j = b_cj, the coefficient (each non-reference
#             category's log-odds against the reference), SE its own.
# The delta-method gradient in b_la (non-reference block l, column a):
#   x_ia pi_ic [(d_cl - pi_il)(b_cj - bbar_ij) - pi_il (b_lj - bbar_ij)]
#     + (a == j) pi_ic (d_cl - pi_il)
# (checked against a central difference of the effect). V: solve(-H)
# (classical), its hc1 sandwich or CR1 over g (the score (y - pi) (x) x,
# gen_multinomial.R's mn_robust). Wald z, conf 0.95. The EXTERNAL
# cross-check is marginaleffects avg_slopes / slopes(type = "probs") on
# multinom with its weights set to the polished MLE, the same V, and the
# Richardson-extrapolated Jacobian of the ordinal block. Rows are [x1, x2,
# y, w, g], y the 0-based index of the row's level in `levels` (the
# dictionary order); the fitted categories are `levels` in that order,
# the reference first in multinom's factor. Effects are outcome-major.
# Seeded apart (set.seed(20261118)), after every case above.

mm_parts <- function(beta, X, Yi, w) {
  k <- ncol(Yi) - 1; q <- ncol(X)
  B <- matrix(beta, nrow = q)
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

mm_newton <- function(beta, X, Yi, w) {
  for (it in 1:100) {
    pp <- mm_parts(beta, X, Yi, w)
    step <- solve(pp$H, pp$g)
    beta <- beta + step
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(beta)))) break
  }
  if (max(abs(mm_parts(beta, X, Yi, w)$g)) > 1e-9) stop("multinomial Newton polish did not converge")
  beta
}

# mm_point: the per-category effects of every term at the points pts
# (rows, intercept first) with weights ww, and their gradients in beta
# (category order = the fitted order, reference first).
mm_point <- function(beta, pts, ww, K, terms, bin = rep(FALSE, length(terms))) {
  q <- ncol(pts)
  B <- cbind(0, matrix(beta, nrow = q))   # q x K, reference column 0
  eta <- pts %*% B
  P <- exp(eta - apply(eta, 1, max)); P <- P / rowSums(P)
  est <- matrix(0, K, length(terms)); jac <- array(0, c(K, length(terms), length(beta)))
  probs <- function(pp) {
    e <- pp %*% B
    Q <- exp(e - apply(e, 1, max))
    Q / rowSums(Q)
  }
  for (t in seq_along(terms)) {
    j <- t + 1
    if (bin[t]) {
      # binary contrast: pi_c(x_j = 1) - pi_c(x_j = 0), gradient
      # x_a pi_c (d_cl - pi_l) differenced over the two points.
      p1 <- pts; p1[, j] <- 1
      p0 <- pts; p0[, j] <- 0
      P1 <- probs(p1); P0 <- probs(p0)
      for (c in 1:K) {
        est[c, t] <- sum(ww * (P1[, c] - P0[, c])) / sum(ww)
        for (l in 2:K) {
          cols <- (l - 2) * q + 1:q
          jac[c, t, cols] <- colSums(ww * (P1[, c] * ((c == l) - P1[, l]) * p1 - P0[, c] * ((c == l) - P0[, l]) * p0)) / sum(ww)
        }
      }
      next
    }
    bbar <- drop(P %*% B[j, ])
    for (c in 1:K) {
      dc <- B[j, c] - bbar
      est[c, t] <- sum(ww * P[, c] * dc) / sum(ww)
      for (l in 2:K) {
        g <- (c == l) - P[, l]
        A <- P[, c] * (g * dc - P[, l] * (B[j, l] - bbar))
        cols <- (l - 2) * q + 1:q
        jac[c, t, cols] <- colSums(ww * A * pts) / sum(ww)
        jac[c, t, (l - 2) * q + j] <- jac[c, t, (l - 2) * q + j] + sum(ww * P[, c] * g) / sum(ww)
      }
    }
  }
  list(est = est, jac = jac)
}

mm_cases <- list()

# mm_case fits one case: d has x1, x2, y (0-based into levels), w, g.
mm_case <- function(name, d, levels, reference = NULL, type = "ame", scale = "response", covariance = "classical", kind = "",
                    binary = "slope") {
  w <- if (kind == "") rep(1, nrow(d)) else d$w
  if (kind == "probability") w <- w * (sum(w)^2 / sum(w^2)) / sum(w)
  ref <- if (is.null(reference)) levels[1] else reference
  fitted_levels <- c(ref, setdiff(levels, ref))
  lab <- levels[d$y + 1]
  if (!all(levels %in% lab)) stop(name, ": empty level")
  yy <- factor(lab, levels = fitted_levels)
  dd <- data.frame(x1 = d$x1, x2 = d$x2, yy = yy, .w = w)
  m <- suppressWarnings(multinom(yy ~ x1 + x2, data = dd, weights = .w, Hess = TRUE, maxit = 1000, reltol = 1e-16, trace = FALSE))
  X <- cbind(1, d$x1, d$x2)
  Yi <- sapply(fitted_levels, function(l) as.numeric(lab == l))
  beta <- mm_newton(as.vector(t(coef(m))), X, Yi, w)
  mg_check(paste(name, "multinom vs polished MLE"), as.vector(t(coef(m))), beta, 1e-6)
  pp <- mm_parts(beta, X, Yi, w)
  Hinv <- solve(pp$H)
  K <- length(fitted_levels); q <- ncol(X); dd_ <- length(beta)
  nstar <- sum(w)
  R <- Yi[, -1, drop = FALSE] - pp$P[, -1, drop = FALSE]
  S <- do.call(cbind, lapply(seq_len(ncol(R)), function(k) X * R[, k]))
  V <- switch(covariance,
    classical = Hinv,
    hc1 = Hinv %*% crossprod(S, S * w) %*% Hinv * nstar / (nstar - dd_),
    cluster = {
      Sg <- rowsum(S * w, d$g)
      G <- nrow(Sg)
      Hinv %*% crossprod(Sg) %*% Hinv * G / (G - 1) * (nstar - 1) / (nstar - dd_)
    })
  terms <- c("x1", "x2")
  bin <- binary == "contrast" & c(mg_isbin(d$x1), mg_isbin(d$x2))
  pts <- if (type == "mem") matrix(colSums(w * X) / sum(w), nrow = 1) else X
  ww <- if (type == "mem") 1 else w
  # Effects in dictionary order (the Go fit's categories).
  ord <- match(levels, fitted_levels)
  if (scale == "link") {
    outcomes <- levels[levels != ref]
    est <- c(); se <- c()
    for (o in outcomes) {
      blk <- match(o, fitted_levels) - 1
      for (t in seq_along(terms)) {
        i <- (blk - 1) * q + t + 1
        est <- c(est, beta[i]); se <- c(se, sqrt(V[i, i]))
      }
    }
  } else {
    outcomes <- levels
    pt <- mm_point(beta, pts, ww, K, terms, bin)
    if (max(abs(colSums(pt$est))) > 1e-14) stop(name, ": effects of a term do not sum to 0")
    # The gradient against a central difference of the effect.
    h <- 1e-6
    num <- array(0, dim(pt$jac))
    for (a in seq_along(beta)) {
      bp <- beta; bm <- beta; bp[a] <- bp[a] + h; bm[a] <- bm[a] - h
      num[, , a] <- (mm_point(bp, pts, ww, K, terms, bin)$est - mm_point(bm, pts, ww, K, terms, bin)$est) / (2 * h)
    }
    mg_check(paste(name, "delta-method gradient"), pt$jac, num, 1e-7)
    est <- c(); se <- c()
    for (c in ord) for (t in seq_along(terms)) {
      jv <- pt$jac[c, t, ]
      est <- c(est, pt$est[c, t]); se <- c(se, sqrt(drop(t(jv) %*% V %*% jv)))
    }
  }
  stat <- est / se
  zq <- qnorm(0.975)
  out <- list(name = name, type = type, scale = scale, covariance = covariance, weight_kind = kind,
    levels = levels, reference = ref, reference_requested = !is.null(reference),
    outcomes = outcomes, terms = terms,
    rows = lapply(seq_len(nrow(d)), function(i) mg_g17(c(d$x1[i], d$x2[i], d$y[i], d$w[i], d$g[i]))),
    coefficients = mg_g17(beta), vcov = lapply(seq_len(nrow(V)), function(i) mg_g17(V[i, ])),
    estimates = mg_g17(est), std_errors = mg_g17(se), statistics = mg_g17(stat),
    p_values = mg_g17(2 * pnorm(-abs(stat))), ci_lower = mg_g17(est - zq * se), ci_upper = mg_g17(est + zq * se))
  if (type == "mem") out$at <- mg_g17(colSums(w * X[, -1]) / sum(w))
  if (binary == "contrast") {
    out$binary <- binary
    out$contrasts <- I(terms[bin])
  }
  if (kind == "frequency") {
    ex <- d[rep(seq_len(nrow(d)), d$w), ]
    Xe <- cbind(1, ex$x1, ex$x2)
    Ye <- sapply(fitted_levels, function(l) as.numeric(levels[ex$y + 1] == l))
    be <- mm_newton(beta, Xe, Ye, rep(1, nrow(ex)))
    mg_check(paste(name, "weighted vs expanded coefficients"), beta, be)
    Ve <- solve(mm_parts(be, Xe, Ye, rep(1, nrow(ex)))$H)
    mg_check(paste(name, "weighted vs expanded vcov"), Hinv, Ve)
    if (scale == "response") {
      pe <- if (type == "mem") matrix(colSums(Xe) / nrow(Xe), nrow = 1) else Xe
      ef <- mm_point(be, pe, if (type == "mem") 1 else rep(1, nrow(ex)), K, terms, bin)
      mg_check(paste(name, "weighted vs expanded estimates"), mm_point(beta, pts, ww, K, terms, bin)$est, ef$est)
    }
  }
  if (mg_have_me && scale == "response") {
    # multinom at the polished MLE: its wts are the K output units'
    # q + 1 weights each (a masked bias, then the q design columns), the
    # reference unit's masked at 0.
    for (k in 2:K) m$wts[(k - 1) * (q + 1) + 1 + 1:q] <- beta[(k - 2) * q + 1:q]
    mg_check(paste(name, "multinom set to the MLE"), as.vector(t(coef(m))), beta, 1e-15)
    Vn <- V
    dimnames(Vn) <- dimnames(vcov(m))
    old_nd <- options(marginaleffects_numDeriv = list(method = "Richardson", method.args = list(eps = 1e-4, d = 0.03, r = 4)))
    on.exit(options(old_nd), add = TRUE)
    args <- list(m, variables = terms, vcov = Vn, type = "probs", eps = 1e-4)
    if (type == "ame") {
      args$newdata <- dd
      if (kind != "") args$wts <- ".w"
      s <- do.call(marginaleffects::avg_slopes, args)
    } else {
      args$newdata <- data.frame(x1 = sum(w * d$x1) / sum(w), x2 = sum(w * d$x2) / sum(w), .w = 1)
      s <- do.call(marginaleffects::slopes, args)
    }
    key <- paste(s$group, s$term)
    want <- paste(rep(outcomes, each = 2), rep(terms, length(outcomes)))
    s <- s[match(want, key), ]
    if (anyNA(s$estimate)) stop(name, ": marginaleffects did not report every category and term")
    out$me_estimates <- mg_g17(s$estimate)
    out$me_std_errors <- mg_g17(s$std.error)
  }
  mm_cases[[length(mm_cases) + 1]] <<- out
}

suppressPackageStartupMessages(library(nnet))
if (as.character(packageVersion("nnet")) != "7.3.21") stop("gen_margins.R pins nnet 7.3-21, got ", packageVersion("nnet"))
set.seed(20261118)
mm_design <- function(n, B, g = 9) {
  d <- data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(rnorm(n), 3))
  eta <- cbind(0, cbind(1, d$x1, d$x2) %*% B)
  P <- exp(eta) / rowSums(exp(eta))
  d$y <- apply(P, 1, function(p) sample.int(length(p), 1, prob = p)) - 1
  d$w <- 1
  d$g <- rep(seq_len(g), length.out = n)
  d
}
d <- mm_design(160, cbind(c(0.3, 0.8, -0.5), c(-0.4, 1.1, 0.6)))
mm_case("mn_ame", d, c("a", "b", "c"))
mm_case("mn_mem", d, c("a", "b", "c"), type = "mem")
mm_case("mn_link", d, c("a", "b", "c"), scale = "link")
mm_case("mn_mem_link", d, c("a", "b", "c"), type = "mem", scale = "link")
mm_case("mn_hc1", d, c("a", "b", "c"), covariance = "hc1")
mm_case("mn_cluster_mem", d, c("a", "b", "c"), type = "mem", covariance = "cluster")
d <- mm_design(200, cbind(c(0.2, -0.6, 0.4), c(0.5, 0.3, -0.8), c(-0.3, 0.9, 0.2)))
mm_case("mn_ame_reference", d, c("low", "mid", "high", "top"), reference = "high")
mm_case("mn_link_reference", d, c("low", "mid", "high", "top"), reference = "high", scale = "link")
d <- mm_design(70, cbind(c(0.1, 0.7, -0.4), c(-0.2, -0.5, 0.9)), g = 7)
d$w <- sample(1:3, 70, replace = TRUE)
mm_case("mn_ame_freq", d, c("a", "b", "c"), kind = "frequency")
mm_case("mn_mem_freq", d, c("a", "b", "c"), type = "mem", kind = "frequency")
d$w <- round(runif(70, 0.2, 3), 3)
mm_case("mn_ame_prob", d, c("a", "b", "c"), kind = "probability")

# ---- binary contrast (U69 #641) --------------------------------------
# x2 is a 0/1 column; binary = "contrast" reports its mu(1) - mu(0) (the
# closed forms in each block's header), x1 keeps its slope. Seeded apart
# (set.seed(20261123)), after every case above, so none of them moves.
set.seed(20261123)
mc_design <- function(n) data.frame(x1 = round(runif(n, -1, 2), 3), x2 = rbinom(n, 1, 0.45))
d <- mc_design(100)
d$y <- 1 + 0.8 * d$x1 - 0.5 * d$x2 + rnorm(100, sd = 0.7)
mg_case("ols_ame_contrast", "lm", NULL, "identity", d, binary = "contrast")
d <- mc_design(150)
d$y <- rbinom(150, 1, plogis(-0.3 + 0.9 * d$x1 - 0.7 * d$x2))
mg_case("logit_ame_contrast", "glm", binomial("logit"), "logit", d, binary = "contrast")
mg_case("logit_mem_contrast", "glm", binomial("logit"), "logit", d, type = "mem", binary = "contrast")
mg_case("logit_link_contrast", "glm", binomial("logit"), "logit", d, scale = "link", binary = "contrast")
mg_case("logit_hc3_contrast", "glm", binomial("logit"), "logit", d, covariance = "hc3", binary = "contrast")
mg_case("probit_mem_contrast", "glm", binomial("probit"), "probit", d, type = "mem", binary = "contrast")
d <- mc_design(120)
d$y <- rpois(120, exp(0.4 + 0.5 * d$x1 - 0.6 * d$x2))
d$g <- rep(1:12, each = 10)
mg_case("poisson_ame_contrast", "glm", poisson("log"), "log", d, binary = "contrast")
mg_case("poisson_cluster_contrast", "glm", poisson("log"), "log", d, covariance = "cluster", cluster = TRUE, binary = "contrast")
d <- mc_design(100)
d$y <- rgamma(100, shape = 4, rate = 4 / exp(0.3 + 0.2 * d$x1 + 0.25 * d$x2))
mg_case("gamma_log_ame_contrast", "glm", Gamma("log"), "log", d, binary = "contrast")
d <- mc_design(100)
d$y <- rnbinom(100, size = 2, mu = exp(0.6 + 0.5 * d$x1 - 0.4 * d$x2))
mg_case("nb_ame_contrast", "nb", NULL, "log", d, binary = "contrast")
d <- mc_design(50)
d$y <- rbinom(50, 1, plogis(0.2 + 1.1 * d$x1 - 0.8 * d$x2))
d$f <- sample(1:4, 50, replace = TRUE)
mg_case("logit_ame_freq_contrast", "glm", binomial("logit"), "logit", d, weighted = TRUE, binary = "contrast")
mg_case("logit_mem_freq_contrast", "glm", binomial("logit"), "logit", d, type = "mem", weighted = TRUE, binary = "contrast")

mo_design_bin <- function(n, J, b, g = 10) {
  d <- mc_design(n)
  eta <- b[1] * d$x1 + b[2] * d$x2
  cuts <- qlogis(seq_len(J - 1) / J)
  d$y <- 1 + rowSums(outer(eta + rlogis(n), cuts, ">"))
  d$g <- rep(seq_len(g), length.out = n)
  d$w <- 1
  d
}
d <- mo_design_bin(160, 4, c(0.9, -0.8))
mo_case("ord_logit_ame_contrast", d, binary = "contrast")
mo_case("ord_probit_mem_contrast", d, link = "probit", type = "mem", binary = "contrast")
mo_case("ord_logit_link_contrast", d, scale = "link", binary = "contrast")
mo_case("ord_logit_hc1_contrast", d, covariance = "hc1", binary = "contrast")
d <- mo_design_bin(70, 3, c(1.1, -0.9), g = 7)
d$w <- sample(1:3, 70, replace = TRUE)
mo_case("ord_logit_ame_freq_contrast", d, weighted = TRUE, binary = "contrast")

mm_design_bin <- function(n, B, g = 9) {
  d <- mc_design(n)
  eta <- cbind(0, cbind(1, d$x1, d$x2) %*% B)
  P <- exp(eta) / rowSums(exp(eta))
  d$y <- apply(P, 1, function(p) sample.int(length(p), 1, prob = p)) - 1
  d$w <- 1
  d$g <- rep(seq_len(g), length.out = n)
  d
}
d <- mm_design_bin(180, cbind(c(0.3, 0.8, -0.7), c(-0.4, 1.1, 0.6)))
mm_case("mn_ame_contrast", d, c("a", "b", "c"), binary = "contrast")
mm_case("mn_mem_contrast", d, c("a", "b", "c"), type = "mem", binary = "contrast")
mm_case("mn_link_contrast", d, c("a", "b", "c"), scale = "link", binary = "contrast")
mm_case("mn_cluster_contrast", d, c("a", "b", "c"), covariance = "cluster", binary = "contrast")
d <- mm_design_bin(70, cbind(c(0.1, 0.7, -0.6), c(-0.2, -0.5, 0.9)), g = 7)
d$w <- sample(1:3, 70, replace = TRUE)
mm_case("mn_ame_freq_contrast", d, c("a", "b", "c"), kind = "frequency", binary = "contrast")

pk <- list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
  MASS = as.character(packageVersion("MASS")), sandwich = as.character(packageVersion("sandwich")))
if (mg_have_me) {
  pk$marginaleffects <- as.character(packageVersion("marginaleffects"))
  pk$insight <- as.character(packageVersion("insight"))
  pk$data.table <- as.character(packageVersion("data.table"))
  pk$generics <- as.character(packageVersion("generics"))
  pk$Matrix <- as.character(packageVersion("Matrix"))
}
pk$nnet <- as.character(packageVersion("nnet"))
write_golden("margins", list(
  generator = "scripts/reference/gen_margins.R",
  r_version = R.version.string,
  packages = pk,
  note = paste(
    "y ~ x1 + x2; terms x1, x2. rows are [x1, x2, y] (+ g when cluster, + f when weighted).",
    "estimates .. ci_upper, df, at: the analytic closed forms (header) at the fit's fixed point, conf_level 0.95;",
    "df present where the statistic is a Student t (lm; gamma under the classical vcov).",
    "vcov: the covariance the effects read (vcov | sandwich vcovHC HC3 | vcovCL HC1 over g).",
    "me_estimates / me_std_errors: marginaleffects avg_slopes / slopes(newdata = 'mean'), numerical derivatives.",
    "_freq: glm(weights = f), equal to its rep() expansion (checked).",
    "_derived: x1 = log1p(z), x2 = (g == 'b'), pre-computed; raw carries [z, g index] per row.",
    "binary / contrasts (every block): binary = 'contrast' reports each listed 0/1 term as mu(1) - mu(0) (absent: slopes)."),
  cases = mg_cases,
  ordinal_note = paste(
    "REG_ORDINAL (polr, y ~ x1 + x2): per-level effects, outcome-major (for each outcome, for each term); rows are [x1, x2, y, w, g].",
    "estimates .. ci_upper, at: the closed forms (the ordinal block's header) at the polished MLE, Wald z, conf_level 0.95;",
    "vcov: the covariance the effects read (analytic inverse information | hc1 | CR1 over g), over c(beta, zeta).",
    "scale link: the effect on each cumulative link g(P(Y <= k)), k < K (outcomes lists them); no marginaleffects counterpart.",
    "me_estimates / me_std_errors: marginaleffects avg_slopes / slopes at the weighted means on polr set to the MLE.",
    "_freq: polr(weights = w), equal to its rep() expansion (checked)."),
  ordinal_cases = mo_cases,
  multinomial_note = paste(
    "REG_MULTINOMIAL (multinom, y ~ x1 + x2): per-category effects, outcome-major; rows are [x1, x2, y, w, g], y the 0-based index into levels (the dictionary order).",
    "estimates .. ci_upper, at: the closed forms (the multinomial block's header) at the polished MLE, Wald z, conf_level 0.95;",
    "coefficients / vcov: the polished MLE (category-major over the non-reference levels in multinom's order: reference first, then levels) and the covariance the effects read.",
    "scale response: one effect per level (dictionary order); scale link: per non-reference level, the coefficient; no marginaleffects counterpart.",
    "me_estimates / me_std_errors: marginaleffects avg_slopes / slopes(type = 'probs') at the weighted means on multinom set to the MLE.",
    "_freq: multinom(weights = w), equal to its rep() expansion (checked); _prob: w* = w n_eff / sum(w)."),
  multinomial_cases = mm_cases
))
