# gen_glm_gamma.R — R oracle for REG_GLM gamma inference under the
# ESTIMATED dispersion (reference-oracles E2-S3, #271).
#
# Writes <out_dir>/glm_gamma.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_glm_gamma.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base stats +
# jsonlite only.
#
# Every case is glm(y ~ x1 + x2, family = Gamma()) — the inverse link,
# R's default and the one Pulse implements — read through R's DEFAULT
# summary.glm: the dispersion estimated as the Pearson chi-square over
# the residual df, phi = sum(w (y - mu)^2 / mu^2) / (n - k), the standard
# errors sqrt(phi diag((X'WX)^-1)), t = b / se on n - k df, p =
# 2 pt(-|t|, n - k), vcov(g) = phi (X'WX)^-1. The intervals are the
# t-based Wald interval b -/+ qt(1 - a/2, n - k) se at 0.95 and 0.90 (the
# basis of summary.glm's t test; confint.default would read qnorm).
#
# As in gen_glm_irls.R, the figures come from a refit warm-started at the
# converged coefficients: glm's working weights lag one IRLS update, and
# summary.glm's dispersion reads those weights, so without the refit
# both the SEs and phi sit ~1e-8 off the MLE's. Self-checks: summary's
# dispersion equals sum(residuals(g, "pearson")^2) / df.residual(g), and
# its p-values equal 2 pt(-|t|, df), at relative 1e-10.
#
# Seeded (set.seed(2603)) draws on its own after every other golden, so
# no earlier golden moves. Rows are written out as {x1, x2, y}.

suppressPackageStartupMessages(library(jsonlite))

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

gg_num <- function(x) {
  if (length(x) != 1) stop("gg_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
gg_vec <- function(v) lapply(unname(as.numeric(v)), gg_num)
gg_mat <- function(m) lapply(seq_len(nrow(m)), function(i) gg_vec(m[i, ]))
gg_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (!all(abs(a - b) <= tol * pmax(abs(a), abs(b)))) {
    stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / pmax(abs(a), abs(b))))
  }
}

set.seed(2603)
gg_cases <- list()

# gg_case fits y ~ x1 + x2 by Gamma(inverse) and records summary.glm's
# estimated-dispersion inference.
gg_case <- function(name, d, note) {
  ctl <- glm.control(epsilon = 1e-14, maxit = 100)
  g <- glm(y ~ x1 + x2, family = Gamma(), data = d, control = ctl)
  if (!g$converged) stop(name, ": glm did not converge")
  g <- glm(y ~ x1 + x2, family = Gamma(), data = d, start = coef(g), control = ctl)
  if (!g$converged) stop(name, ": warm-started refit did not converge")
  s <- summary(g)
  df <- g$df.residual
  phi <- s$dispersion
  gg_near(phi, sum(residuals(g, "pearson")^2) / df, 1e-10, paste(name, "dispersion vs Pearson chi-square / df"))
  tv <- s$coefficients[, 3]
  gg_near(s$coefficients[, 4], 2 * pt(-abs(tv), df), 1e-10, paste(name, "p vs 2 pt(-|t|, df)"))
  b <- coef(g); se <- s$coefficients[, 2]
  ci <- function(level) {
    q <- qt(0.5 + level / 2, df)
    list(level = gg_num(level), lower = gg_vec(b - q * se), upper = gg_vec(b + q * se))
  }
  rows <- lapply(seq_len(nrow(d)), function(i) list(x1 = gg_num(d$x1[i]), x2 = gg_num(d$x2[i]), y = gg_num(d$y[i])))
  gg_cases[[length(gg_cases) + 1]] <<- list(
    name = name, note = note, family = "gamma", link = "inverse",
    target = "y", predictors = I(c("x1", "x2")), terms = I(c("(intercept)", "x1", "x2")),
    n_rows = gg_num(nrow(d)), rows = rows,
    coefficients = gg_vec(b), std_errors = gg_vec(se), t_values = gg_vec(tv),
    p_values = gg_vec(s$coefficients[, 4]),
    dispersion = gg_num(phi), df_residual = gg_num(df),
    dispersion_one_std_errors = gg_vec(summary(g, dispersion = 1)$coefficients[, 2]),
    vcov = gg_mat(vcov(g)),
    intervals = list(ci(0.95), ci(0.90)),
    deviance = gg_num(deviance(g)), null_deviance = gg_num(g$null.deviance))
}

# A moderate design: shape 4 (true phi 0.25), 40 rows.
gg_n <- 40
gg_x1 <- round(runif(gg_n, 0, 2), 3)
gg_x2 <- round(rnorm(gg_n), 3)
gg_mu <- 1 / (0.4 + 0.3 * gg_x1 + 0.05 * gg_x2)
gg_case("gamma_shape4", data.frame(x1 = gg_x1, x2 = gg_x2,
  y = round(rgamma(gg_n, shape = 4, rate = 4 / gg_mu), 4)),
  "shape 4 (phi near 0.25): SEs half the dispersion-1 ones")

# Heavy dispersion: shape 0.8 (true phi 1.25), 25 rows.
gg_n <- 25
gg_x1 <- round(runif(gg_n, 0, 3), 3)
gg_x2 <- round(rnorm(gg_n), 3)
gg_mu <- 1 / (0.5 + 0.2 * gg_x1 - 0.04 * gg_x2)
gg_case("gamma_shape08", data.frame(x1 = gg_x1, x2 = gg_x2,
  y = round(rgamma(gg_n, shape = 0.8, rate = 0.8 / gg_mu), 4)),
  "shape 0.8 (phi near 1.25): SEs above the dispersion-1 ones")

# Few residual df: 5 rows, df = 2, where the t tail differs most from z.
gg_case("gamma_df2", data.frame(x1 = c(0.2, 0.9, 1.4, 2.1, 2.8), x2 = c(1.1, -0.4, 0.3, -1.2, 0.6),
  y = c(2.31, 1.27, 1.05, 0.62, 0.71)),
  "n = 5, k = 3: df 2, the t reference far from the normal")

gg_doc <- list(
  generator = "scripts/reference/gen_glm_gamma.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_GLM gamma (inverse link): estimated dispersion, t-based inference",
  r_function = "g <- glm(y ~ x1 + x2, family = Gamma(), control = glm.control(epsilon = 1e-14, maxit = 100)); g <- glm(..., start = coef(g)); summary(g); vcov(g)",
  note = paste(
    "summary.glm defaults: dispersion = Pearson chi-square / df.residual, std_errors = sqrt(dispersion * diag((X'WX)^-1)), p = 2 pt(-|t|, df.residual), vcov = dispersion * (X'WX)^-1.",
    "intervals: b -/+ qt(0.5 + level/2, df.residual) * se. dispersion_one_std_errors: summary(dispersion = 1), the pre-U36 Pulse figure, kept for the before/after record.",
    "Figures from a refit warm-started at the converged coefficients (evaluated at the MLE)."),
  cases = gg_cases)
write_golden("glm_gamma", gg_doc)
