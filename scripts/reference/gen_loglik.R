# gen_loglik.R — R oracle for the maximised log-likelihood and AIC a
# plain REG_OLS / REG_GLM fit reports (RegressionResult two_log_lik /
# aic; U65 E2-S2, owner-delegated): R's logLik() and AIC() exactly.
#
# Writes <out_dir>/loglik.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_loglik.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite only.
#
# The definitions (each re-derived from its closed form here; the script
# stops on a mismatch):
#   lm        logLik.lm: 0.5 * (-N (log(2 pi) + 1 - log N + log RSS)),
#             df = rank + 1 (sigma counted); y ~ x1 + x2 and through the
#             origin (y ~ 0 + x1 + x2, rank p)
#   binomial  family$aic: -2 sum log dbinom(y; 1, mu), df = rank
#   poisson   family$aic: -2 sum log dpois(y; mu), df = rank
#   gamma     family$aic: -2 sum log dgamma(y; 1/phi, scale mu phi) + 2,
#             phi = deviance / n (NOT summary.glm's Pearson phi), so
#             logLik.glm's df = rank + 1 (phi counted)
#   AIC = -2 logLik + 2 df; two_log_lik = 2 logLik.
# Frequency-weighted cases ("_freq", integer f) are the rep()-expanded
# fit's figures: glm(weights = f)'s family aic equals the expansion's
# (asserted), lm(weights = f)'s logLik does not (logLik.lm reads the
# weights as precision weights, N = the row count), so the lm golden is
# the expanded lm's — Pulse's frequency rule (weighting.md). Probability
# weights have no oracle: Pulse reports no likelihood there.
#
# GLM fits are refitted from their own optimum until the coefficients
# stop moving (glm.control(epsilon = 1e-14)); the canonical links
# converge quadratically, so a few refits reach the fixed point.
#
# Seeded (set.seed(20261108)); draws its own rows, so no other golden
# moves. Rows are [x1, x2, y] or [x1, x2, y, f].

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
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_loglik.R pins R 4.6.1, got ", R.version.string)

ll_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
ll_check <- function(what, got, want, tol = 1e-12) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  if (max(abs(got - want) / pmax(1, abs(want))) > tol) stop(what, ": closed form disagrees with R")
}
ll_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# ll_glm fits glm(y ~ x1 + x2, family) on d, refitted to its fixed point.
ll_glm <- function(fam, d) {
  m <- glm(y ~ x1 + x2, family = fam, data = d, control = ll_ctl)
  for (k in 1:50) {
    b <- coef(m)
    m <- glm(y ~ x1 + x2, family = fam, data = d, control = ll_ctl, start = b)
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-15) break
  }
  if (!m$converged) stop("glm did not converge")
  m
}

# ll_closed is the closed-form logLik of model m on its own rows.
ll_closed <- function(family, m) {
  y <- m$y
  if (family == "gaussian") {
    n <- length(residuals(m))
    rss <- sum(residuals(m)^2)
    return(0.5 * (-n * (log(2 * pi) + 1 - log(n) + log(rss))))
  }
  mu <- fitted(m)
  switch(family,
    binomial = sum(dbinom(y, 1, mu, log = TRUE)),
    poisson = sum(dpois(y, mu, log = TRUE)),
    gamma = {
      disp <- deviance(m) / length(y)
      sum(dgamma(y, shape = 1 / disp, scale = mu * disp, log = TRUE))
    })
}

ll_cases <- list()

ll_case <- function(name, family, d, intercept = TRUE, fw = NULL) {
  fit <- function(dd) {
    if (family == "gaussian") {
      if (intercept) lm(y ~ x1 + x2, data = dd) else lm(y ~ 0 + x1 + x2, data = dd)
    } else {
      ll_glm(switch(family, binomial = binomial(), poisson = poisson(), gamma = Gamma()), dd)
    }
  }
  ex <- if (is.null(fw)) d else d[rep(seq_len(nrow(d)), fw), ]
  m <- fit(ex)
  ll <- logLik(m)
  ll_check(paste(name, "logLik closed form"), ll_closed(family, m), as.numeric(ll))
  df <- attr(ll, "df")
  want_df <- m$rank + if (family %in% c("gaussian", "gamma")) 1 else 0
  if (df != want_df) stop(name, ": logLik df ", df, ", want ", want_df)
  ll_check(paste(name, "AIC"), -2 * as.numeric(ll) + 2 * df, AIC(m))
  if (!is.null(fw) && family != "gaussian") {
    # glm(weights = f)'s family aic is the expansion's.
    dw <- d; dw$.f <- fw
    mw <- glm(y ~ x1 + x2, family = m$family, data = dw, weights = .f, control = ll_ctl, start = coef(m))
    ll_check(paste(name, "weighted vs expanded coefficients"), coef(mw), coef(m), 1e-10)
    ll_check(paste(name, "weighted vs expanded AIC"), AIC(mw), AIC(m), 1e-10)
  }
  rows <- if (is.null(fw)) {
    lapply(seq_len(nrow(d)), function(i) ll_g17(c(d$x1[i], d$x2[i], d$y[i])))
  } else {
    lapply(seq_len(nrow(d)), function(i) ll_g17(c(d$x1[i], d$x2[i], d$y[i], fw[i])))
  }
  b <- coef(m)
  if (!intercept) b <- c(0, b)
  ll_cases[[length(ll_cases) + 1]] <<- list(
    name = name, family = family, intercept = intercept, weighted = !is.null(fw), rows = rows,
    coefficients = ll_g17(b), two_log_lik = ll_g17(2 * as.numeric(ll)), aic = ll_g17(AIC(m)), df = ll_g17(df))
}

set.seed(20261108)
ll_design <- function(n) data.frame(x1 = round(runif(n, 0, 2), 3), x2 = round(runif(n, -1, 1), 3))

d <- ll_design(60)
d$y <- round(1.5 + 0.8 * d$x1 - 0.6 * d$x2 + rnorm(60, sd = 0.7), 4)
ll_case("ols", "gaussian", d)
ll_case("ols_origin", "gaussian", d, intercept = FALSE)
ll_case("ols_freq", "gaussian", d[1:30, ], fw = sample(1:4, 30, replace = TRUE))

d <- ll_design(80)
d$y <- rbinom(80, 1, plogis(-0.4 + 0.9 * d$x1 - 0.7 * d$x2))
ll_case("binomial", "binomial", d)
ll_case("binomial_freq", "binomial", d[1:40, ], fw = sample(1:4, 40, replace = TRUE))

d <- ll_design(70)
d$y <- rpois(70, exp(0.3 + 0.6 * d$x1 - 0.4 * d$x2))
ll_case("poisson", "poisson", d)
ll_case("poisson_freq", "poisson", d[1:35, ], fw = sample(1:4, 35, replace = TRUE))

d <- ll_design(60)
d$y <- round(rgamma(60, shape = 3, rate = 3 * (0.5 + 0.3 * d$x1 + 0.2 * d$x2)), 5)
ll_case("gamma", "gamma", d)
ll_case("gamma_freq", "gamma", d[1:30, ], fw = sample(1:4, 30, replace = TRUE))

write_golden("loglik", list(
  generator = "scripts/reference/gen_loglik.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite"))),
  note = paste(
    "two_log_lik = 2 logLik(m), aic = AIC(m), df = attr(logLik(m), 'df'); y ~ x1 + x2 (gaussian: lm; ols_origin: y ~ 0 + x1 + x2,",
    "coefficients[0] = 0) or glm(family) on its canonical link. coefficients (intercept), x1, x2.",
    "_freq: every figure the rep()-expanded fit's (rows [x1, x2, y, f])."),
  cases = ll_cases
))
