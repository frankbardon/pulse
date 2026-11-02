# gen_margins.R — R oracle for the regression marginal effects
# (RegressionSpec.Margins, U65 E3-S1, #627): average marginal effects
# (ame) and marginal effects at the mean (mem) on REG_OLS and REG_GLM,
# response and link scale, with delta-method standard errors on the fit's
# own coefficient covariance (classical, sandwich HC3, cluster).
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
mg_effects <- function(m, link, type, scale, V, df, w) {
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
                    weighted = FALSE, cluster = FALSE) {
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
  eff <- mg_effects(m, link, type, scale, V, df, w)
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
    effe <- mg_effects(me, link, type, scale, Ve, df, rep(1, nrow(ex)))
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
# "b" column). Each effect is the slope in the column itself (a unit
# change at the derivative, Stata's dydx on a continuous-coded dummy),
# so the fit and its effects are an ordinary logit on (x1, x2); the case
# also carries the raw z and g ("raw": [z, g index], g "a" = 0, "b" = 1)
# so the root test builds the same columns through the features.
d <- data.frame(z = round(runif(120, 0, 6), 3), g = sample(0:1, 120, replace = TRUE))
d$x1 <- log1p(d$z)
d$x2 <- as.numeric(d$g == 1)
d$y <- rbinom(120, 1, plogis(-0.8 + 0.7 * d$x1 - 0.9 * d$x2))
mg_case("logit_ame_derived", "glm", binomial("logit"), "logit", d)
mg_cases[[length(mg_cases)]]$raw <- lapply(seq_len(nrow(d)), function(i) mg_g17(c(d$z[i], d$g[i])))
# marginaleffects detects a 0/1 column and reports its discrete 1 - 0
# contrast, not the slope Pulse (and the closed form above) reports for
# it: its x2 cross-check figure is that contrast, flagged so the Go test
# compares x1 only (the slope is pinned by the closed form).
mg_cases[[length(mg_cases)]]$me_binary_terms <- I("x2")

pk <- list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
  MASS = as.character(packageVersion("MASS")), sandwich = as.character(packageVersion("sandwich")))
if (mg_have_me) {
  pk$marginaleffects <- as.character(packageVersion("marginaleffects"))
  pk$insight <- as.character(packageVersion("insight"))
  pk$data.table <- as.character(packageVersion("data.table"))
  pk$generics <- as.character(packageVersion("generics"))
  pk$Matrix <- as.character(packageVersion("Matrix"))
}
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
    "_derived: x1 = log1p(z), x2 = (g == 'b'), pre-computed; raw carries [z, g index] per row."),
  cases = mg_cases
))
