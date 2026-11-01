# gen_glm_influence.R — R oracle for the influence figures of a BOUND
# regression fit (U65 E1-S2): ATTR_REG_LEVERAGE, ATTR_REG_RESIDUAL kind
# standardized / studentized and ATTR_REG_INFLUENCE measure cooks /
# dffits with params.fit over a REG_OLS or REG_GLM fit.
#
# Writes <out_dir>/glm_influence.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_glm_influence.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite only.
#
# Per case (unweighted, y ~ x1 + x2, glm.control(epsilon = 1e-14),
# refitted from its own optimum: glm_exact),
# R 4.6.1's definitions, with rP the Pearson residual, φ
# summary(m)$dispersion (1 under binomial / poisson), df = n − p′ and
# σ₍ᵢ₎ = √((Σ rP² − rPᵢ²/(1 − hᵢ))/(df − 1)) the leave-one-out σ of
# lm.influence (its weighted.residuals are the Pearson residuals):
#   hat           hatvalues(m)       diag of W½X(XᵀWX)⁻¹XᵀW½, W the final
#                                    IRLS working weights
#   standardized  rstandard(m)       rP / (√φ·√(1 − h)) (type "pearson",
#                                    the 4.6.1 default)
#   standardized_deviance
#                 rstandard(m, type = "deviance")
#                                    rD / (√φ·√(1 − h)), rD = sign(y − μ)·√dᵢ
#                                    the deviance residual (lm: = standardized)
#   studentized   rstudent(m)        rP / (σ₍ᵢ₎·√(1 − h)) when φ is
#                                    estimated, else the standardized
#   cooks         cooks.distance(m)  (rP/(1 − h))²·h/(φ·p′)
#   dffits        dffits(m)          s·rP·√h / (σ·(1 − h)), σ = σ₍ᵢ₎ when φ
#                                    is estimated, else √φ; s = sign g′(μ)
#                                    (−1 under the gamma inverse link)
# The script re-derives each figure from those closed forms and stops
# on a mismatch, so the golden also pins the formulas Pulse implements.
# "ols" is lm(); the gaussian GLM case must agree with it.

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_glm_influence.R pins R 4.6.1, got ", R.version.string)

g17 <- function(v) structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")

check <- function(name, got, want) {
  if (max(abs(got - want) / pmax(1, abs(want))) > 1e-10) stop(name, ": closed form disagrees with R")
}

influence_case <- function(name, family, link, d, m) {
  h <- hatvalues(m)
  rs <- rstandard(m)
  rsd <- if (inherits(m, "glm")) rstandard(m, type = "deviance") else rstandard(m)
  rt <- rstudent(m)
  cd <- cooks.distance(m)
  df <- dffits(m)
  if (any(!is.finite(c(h, rs, rsd, rt, cd, df)))) stop(name, ": non-finite figure")
  # Re-derive from the closed forms (the formulas Pulse implements).
  phi <- if (inherits(m, "glm")) summary(m)$dispersion else summary(m)$sigma^2
  pres <- residuals(m, type = "pearson")
  p1 <- m$rank
  n <- nrow(d)
  dfr <- n - p1
  est <- !(family %in% c("binomial", "poisson"))
  sig_i <- sqrt((sum(pres^2) - pres^2 / (1 - h)) / (dfr - 1))
  std <- pres / (sqrt(phi) * sqrt(1 - h))
  check(paste(name, "standardized"), std, rs)
  dres <- if (inherits(m, "glm")) residuals(m, type = "deviance") else residuals(m)
  check(paste(name, "standardized_deviance"), dres / (sqrt(phi) * sqrt(1 - h)), rsd)
  check(paste(name, "studentized"), if (est) pres / (sig_i * sqrt(1 - h)) else std, rt)
  check(paste(name, "cooks"), (pres / (1 - h))^2 * h / (phi * p1), cd)
  # dffits reads the weighted working residual √w·(y − μ)·g′(μ) =
  # sign(g′(μ))·rP: the change on the linear-predictor scale, so a
  # decreasing link (gamma inverse) flips its sign against rP.
  sg <- if (inherits(m, "glm")) sign(m$family$mu.eta(m$linear.predictors)) else 1
  check(paste(name, "dffits"), sg * pres * sqrt(h) / ((if (est) sig_i else sqrt(phi)) * (1 - h)), df)
  list(
    name = name, family = family, link = link,
    rows = lapply(seq_len(n), function(i) g17(c(d$x1[i], d$x2[i], d$y[i]))),
    coefficients = g17(unname(coef(m))),
    dispersion = g17(phi),
    hat = g17(unname(h)), standardized = g17(unname(rs)), standardized_deviance = g17(unname(rsd)),
    studentized = g17(unname(rt)),
    cooks = g17(unname(cd)), dffits = g17(unname(df))
  )
}

set.seed(20261103)
design <- function(n) data.frame(x1 = round(rnorm(n), 3), x2 = round(runif(n, -1, 1), 3))
ctl <- glm.control(epsilon = 1e-14, maxit = 200)
# glm() keeps the working weights of the iteration BEFORE its last
# update, so hatvalues / weighted.residuals lag the converged μ by ~√ε
# (1e-7 measured). Refitting twice from the converged coefficients
# brings the stored weights onto the fit's own μ (lag ~1e-16).
glm_exact <- function(formula, family, data) {
  m <- glm(formula, family = family, data = data, control = ctl)
  for (k in 1:2) m <- glm(formula, family = family, data = data, control = ctl, start = coef(m))
  m
}
cases <- list()

d <- design(40)
d$y <- round(1.5 + 0.8 * d$x1 - 0.6 * d$x2 + rnorm(40, 0, 0.5), 4)
d$x1[40] <- 4; d$y[40] <- -3
cases[[length(cases) + 1]] <- influence_case("ols", "gaussian", "identity", d, lm(y ~ x1 + x2, data = d))
cases[[length(cases) + 1]] <- influence_case("gaussian_identity", "gaussian", "identity", d,
  glm_exact(y ~ x1 + x2, gaussian(), d))

d <- design(60)
d$y <- as.numeric(runif(60) < plogis(0.4 + 0.8 * d$x1 - 0.6 * d$x2))
cases[[length(cases) + 1]] <- influence_case("binomial_logit", "binomial", "logit", d,
  glm_exact(y ~ x1 + x2, binomial(), d))

d <- design(50)
d$y <- rpois(50, exp(0.4 + 0.5 * d$x1 - 0.4 * d$x2))
cases[[length(cases) + 1]] <- influence_case("poisson_log", "poisson", "log", d,
  glm_exact(y ~ x1 + x2, poisson(), d))

d <- design(50)
mu <- 1 / (2 + 0.4 * d$x1 - 0.3 * d$x2)
d$y <- round(rgamma(50, shape = 4, rate = 4 / mu), 4)
cases[[length(cases) + 1]] <- influence_case("gamma_inverse", "gamma", "inverse", d,
  glm_exact(y ~ x1 + x2, Gamma(), d))

write_golden("glm_influence", list(
  generator = "scripts/reference/gen_glm_influence.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite"))),
  note = "unweighted y ~ x1 + x2; rows are [x1, x2, y]; figures per row in row order",
  cases = cases
))
