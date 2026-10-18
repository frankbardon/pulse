# gen_glm_irls.R — R oracle for REG_GLM's IRLS loop (model-workbench
# E1-S1, #339): per-row starting values, step-halving, the separation
# refusal.
#
# Writes <out_dir>/glm_irls.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_glm_irls.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base stats +
# jsonlite only.
#
# Every case is a one-predictor design. Over a two-level x (0 / 1) the
# MLE has a CLOSED FORM the R fit is asserted against (relative 1e-10)
# and both are written out:
#   * binomial / logit: b0 = logit(p0), b1 = logit(p1) - logit(p0),
#     SE(b0)^2 = 1/a0 + 1/b0, SE(b1)^2 = 1/a0 + 1/b0 + 1/a1 + 1/b1 over
#     the 2 x 2 cell counts
#   * poisson / log: b0 = log(m0), b1 = log(m1 / m0), SE(b0)^2 = 1/S0,
#     SE(b1)^2 = 1/S0 + 1/S1 over the per-level sums S
#   * gamma / inverse: b0 = 1/m0, b1 = 1/m1 - 1/m0 (coefficients only;
#     std_errors are summary(dispersion = 1), Pulse's fixed dispersion)
# std_errors are summary(glm, dispersion = 1) — Pulse fixes the
# dispersion at 1 — and deviance / null_deviance are glm's.
#
# The rows are `cells` {x, y, n}: n copies of (x, y). collections_2x2 is
# the credit-risk showcase's default x collections_flag table (default
# rate 2.03% without the flag, 92.24% with it): strong, NOT separated —
# it must converge. The separated cases (a complete separation over a
# continuous x, and a 2 x 2 with an empty cell) carry glm's own
# "fitted probabilities numerically 0 or 1 occurred" warning in
# r_warnings; Pulse refuses those with PROCESSING_REGRESSION_SEPARATION
# rather than reporting where the loop happened to stop, so they carry
# no coefficients. epoch_seconds_poisson (x = epoch seconds) has no
# closed form: glm alone is the oracle.

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

gi_num <- function(x) {
  if (length(x) != 1) stop("gi_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
gi_vec <- function(v) lapply(unname(as.numeric(v)), gi_num)
gi_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (!all(abs(a - b) <= tol * pmax(1, abs(a), abs(b)))) {
    stop("self-check failed: ", what, " max diff ", max(abs(a - b)))
  }
}

gi_cases <- list()

# gi_case fits y ~ x on the expanded cells and, unless separated, checks
# the fit against the closed form.
gi_case <- function(name, family, cells, target = "y", predictor = "x") {
  d <- data.frame(x = rep(cells$x, cells$n), y = rep(cells$y, cells$n))
  fam <- switch(family, binomial = binomial(), poisson = poisson(), gamma = Gamma())
  warns <- character(0)
  g <- withCallingHandlers(
    glm(y ~ x, family = fam, data = d, control = glm.control(epsilon = 1e-14, maxit = 100)),
    warning = function(w) {
      warns <<- c(warns, conditionMessage(w))
      invokeRestart("muffleWarning")
    })
  separated <- any(grepl("fitted probabilities numerically 0 or 1", warns, fixed = TRUE))
  cell_list <- lapply(seq_len(nrow(cells)), function(i) list(
    x = gi_num(cells$x[i]), y = gi_num(cells$y[i]), n = gi_num(cells$n[i])))
  out <- list(name = name, family = family, link = fam$link,
              target = target, predictor = predictor,
              terms = I(c("(intercept)", predictor)),
              n_rows = gi_num(nrow(d)), cells = cell_list,
              r_warnings = I(warns), separated = separated)
  two_level <- all(d$x %in% c(0, 1))
  if (!separated && !two_level) {
    se1 <- summary(g, dispersion = 1)$coefficients[, 2]
    out$coefficients <- gi_vec(coef(g))
    out$std_errors <- gi_vec(se1)
    out$deviance <- gi_num(deviance(g))
    out$null_deviance <- gi_num(g$null.deviance)
  }
  if (!separated && two_level) {
    y0 <- d$y[d$x == 0]; y1 <- d$y[d$x == 1]
    cf <- switch(family,
      binomial = {
        a0 <- sum(y0 == 1); b0 <- sum(y0 == 0); a1 <- sum(y1 == 1); b1 <- sum(y1 == 0)
        l0 <- log(a0 / b0); l1 <- log(a1 / b1)
        list(coef = c(l0, l1 - l0),
             se = sqrt(c(1 / a0 + 1 / b0, 1 / a0 + 1 / b0 + 1 / a1 + 1 / b1)))
      },
      poisson = {
        s0 <- sum(y0); s1 <- sum(y1)
        list(coef = c(log(mean(y0)), log(mean(y1) / mean(y0))),
             se = sqrt(c(1 / s0, 1 / s0 + 1 / s1)))
      },
      gamma = list(coef = c(1 / mean(y0), 1 / mean(y1) - 1 / mean(y0)), se = NULL))
    gi_near(coef(g), cf$coef, 1e-10, paste(name, "glm vs closed-form coefficients"))
    se1 <- summary(g, dispersion = 1)$coefficients[, 2]
    if (!is.null(cf$se)) gi_near(se1, cf$se, 1e-8, paste(name, "glm vs closed-form std errors"))
    out$coefficients <- gi_vec(coef(g))
    out$closed_form_coefficients <- gi_vec(cf$coef)
    out$std_errors <- gi_vec(se1)
    if (!is.null(cf$se)) out$closed_form_std_errors <- gi_vec(cf$se)
    out$deviance <- gi_num(deviance(g))
    out$null_deviance <- gi_num(g$null.deviance)
  }
  gi_cases[[length(gi_cases) + 1]] <<- out
}

# The showcase table: 53,412 loans without the flag (1,085 defaults),
# 6,588 with it (6,077 defaults).
gi_case("collections_2x2", "binomial",
  data.frame(x = c(0, 0, 1, 1), y = c(0, 1, 0, 1), n = c(52327, 1085, 511, 6077)),
  target = "default", predictor = "collections_flag")

# A rare event on one level: 3 in 4,000 vs 1,640 in 2,000.
gi_case("rare_level_2x2", "binomial",
  data.frame(x = c(0, 0, 1, 1), y = c(0, 1, 0, 1), n = c(3997, 3, 360, 1640)))

# Strong count contrast: mean 0.25 vs 31.
gi_case("poisson_two_level", "poisson",
  data.frame(x = c(rep(0, 2), rep(1, 10)),
             y = c(0, 1, 28, 31, 35, 29, 33, 30, 27, 32, 34, 31),
             n = c(30, 10, rep(1, 10))))

gi_case("gamma_two_level", "gamma",
  data.frame(x = c(rep(0, 5), rep(1, 4)),
             y = c(1.2, 0.8, 2.5, 1.9, 0.6, 4.1, 3.3, 6.0, 5.2),
             n = rep(1, 9)))

# A predictor on a large scale: epoch seconds (~1.7e9) of 48 timestamps
# through 2024 against a positive count-like y. No closed form; full
# rank, but XᵀWX's raw condition number is ~1e23 — the case the
# equilibrated solve exists for.
gi_i <- 0:47
gi_case("epoch_seconds_poisson", "poisson",
  data.frame(x = as.numeric(as.POSIXct(sprintf("2024-%02d-%02d %02d:15:00", 1 + gi_i %% 12, 1 + (gi_i * 3) %% 28, gi_i %% 24), tz = "UTC")),
             y = ((gi_i * 13) %% 17) + 0.5, n = rep(1, 48)))

# Complete separation: y = 1 exactly when x >= 0.
gi_case("complete_separation", "binomial",
  data.frame(x = seq(-19.5, 19.5, by = 1), y = as.numeric(seq(-19.5, 19.5, by = 1) >= 0),
             n = rep(1, 40)))

# Quasi-complete separation: every x = 1 row is an event (an empty cell).
gi_case("empty_cell_2x2", "binomial",
  data.frame(x = c(0, 0, 1), y = c(0, 1, 1), n = c(50, 10, 25)))

gi_doc <- list(
  generator = "scripts/reference/gen_glm_irls.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_GLM IRLS: convergence on strong predictors, the separation refusal",
  r_function = "glm(y ~ x, family, control = glm.control(epsilon = 1e-14, maxit = 100)); summary(glm, dispersion = 1)",
  note = paste(
    "Two-level x: the MLE has a closed form (binomial logits, poisson log means, gamma inverse means) the glm fit is asserted against at relative 1e-10. epoch_seconds_poisson (x = epoch seconds of 2024 timestamps) has none: glm alone.",
    "std_errors are summary(dispersion = 1). cells {x, y, n} expand to n rows each.",
    "separated = glm warned 'fitted probabilities numerically 0 or 1 occurred'; Pulse refuses those with PROCESSING_REGRESSION_SEPARATION naming the predictor, so they carry no coefficients."),
  cases = gi_cases)
write_golden("glm_irls", gi_doc)
