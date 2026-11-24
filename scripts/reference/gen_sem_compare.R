# gen_sem_compare.R — R oracle for a Compose nested-model comparison of
# two SEM fits (`compare` over two MAT_SEM `fits`, U69 #643).
#
# Each case fits two lavaan models on the brand survey
# (internal/sem/testdata/brand_data.csv, the MAT_SEM lavaan fixtures'
# data) — the restricted one declaring more constraints (a dropped path,
# a fixed covariance, group.equal invariance) — and compares them with
#   ml / dwls  lavTestLRT(m0, m1)                         (standard test:
#              the plain chi-square difference T0 - T1 on d0 - d1)
#   wlsmv      lavTestLRT(m0, m1, method = "satorra.bentler.2001",
#              test = "satorra.bentler")                  (the scaled
#              difference (T0 - T1) / cd, cd = (d0 c0 - d1 c1) / (d0 - d1),
#              c = trace(U Gamma) / df, the mean-adjusted scaling factor)
# — each statistic re-derived from its closed form over the two fits'
# test slots and checked against lavTestLRT's (rounded by lavaan to 10
# decimals) at 1e-9. WLSMV fits carry test = c("scaled.shifted",
# "satorra.bentler"): the estimates and the standard statistic are
# DWLS's, and lavaan records trace(U Gamma) beside both robust tests.
# Ordinal fits use the delta parameterization; ML fits lavaan's
# defaults of fit_lavaan.R (likelihood normal, expected information,
# fixed.x); every grouped fit orders the wave levels ascending.
#
# Writes <out_dir>/sem_compare.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_sem_compare.R internal/processing/testdata/reference
#
# (from the repository root; then append the `// golden-hash:` footer the
# Makefile's reference target writes). CI never runs R: the golden is
# committed.
#
# Pinned: R 4.6.1, lavaan 0.7.3 (the release every internal/sem/testdata
# lavaan fixture records), jsonlite 2.0.0. No random draw.

suppressPackageStartupMessages({
  library(jsonlite)
  library(lavaan)
})

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_sem_compare.R pins R 4.6.1, got ", R.version.string)
sc_pins <- c(lavaan = "0.7.3", jsonlite = "2.0.0")
for (p in names(sc_pins)) {
  if (as.character(packageVersion(p)) != sc_pins[[p]]) stop("gen_sem_compare.R pins ", p, " ", sc_pins[[p]], ", got ", packageVersion(p))
}

sc_num <- function(x) {
  if (!is.finite(x)) stop("non-finite reference value")
  structure(sprintf("%.17g", x), class = "json")
}

sc_data <- read.csv("internal/sem/testdata/brand_data.csv")
sc_data$wave <- factor(sc_data$wave, levels = sort(unique(sc_data$wave)))
sc_q9 <- c("q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3")

sc_cfa <- "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3"
sc_struct <- paste0(sc_cfa, "\nrecommend ~ quality + value + service")
sc_struct0 <- paste0(sc_cfa, "\nrecommend ~ quality + value")
sc_cov0 <- paste0(sc_cfa, "\nquality ~~ 0*service")

# Each case: estimator, group (NULL ungrouped), meanstructure (ML),
# ordered, and each side's model and group.equal.
sc_cases <- list(
  list(name = "ml_dropped_path", estimator = "ml", group = NULL, meanstructure = TRUE,
       restricted = list(model = sc_struct0, group_equal = character(0)),
       full = list(model = sc_struct, group_equal = character(0))),
  list(name = "ml_metric_vs_configural", estimator = "ml", group = "wave", meanstructure = TRUE,
       restricted = list(model = sc_struct, group_equal = c("loadings")),
       full = list(model = sc_struct, group_equal = character(0))),
  list(name = "ml_scalar_vs_metric", estimator = "ml", group = "wave", meanstructure = TRUE,
       restricted = list(model = sc_struct, group_equal = c("loadings", "intercepts")),
       full = list(model = sc_struct, group_equal = c("loadings"))),
  list(name = "dwls_fixed_covariance", estimator = "dwls", group = NULL, ordered = sc_q9,
       restricted = list(model = sc_cov0, group_equal = character(0)),
       full = list(model = sc_cfa, group_equal = character(0))),
  list(name = "wlsmv_fixed_covariance", estimator = "wlsmv", group = NULL, ordered = sc_q9,
       restricted = list(model = sc_cov0, group_equal = character(0)),
       full = list(model = sc_cfa, group_equal = character(0))),
  list(name = "wlsmv_thload_vs_configural", estimator = "wlsmv", group = "wave", ordered = sc_q9,
       restricted = list(model = sc_cfa, group_equal = c("thresholds", "loadings")),
       full = list(model = sc_cfa, group_equal = character(0)))
)

sc_fit <- function(cs, side) {
  geq <- if (length(side$group_equal)) side$group_equal else ""
  if (cs$estimator == "ml") {
    return(sem(side$model, data = sc_data, meanstructure = cs$meanstructure, estimator = "ML",
               likelihood = "normal", information = "expected", se = "standard", fixed.x = TRUE,
               group = cs$group, group.equal = geq))
  }
  tests <- if (cs$estimator == "wlsmv") c("scaled.shifted", "satorra.bentler") else "standard"
  sem(side$model, data = sc_data, ordered = cs$ordered, estimator = toupper(cs$estimator),
      parameterization = "delta", test = tests, group = cs$group, group.equal = geq)
}

sc_test <- function(fit, name) {
  for (t in fit@test) if (identical(t$test, name)) return(t)
  stop("no ", name, " test")
}

sc_out <- lapply(sc_cases, function(cs) {
  m0 <- withCallingHandlers(sc_fit(cs, cs$restricted), warning = function(w) stop(conditionMessage(w)))
  m1 <- withCallingHandlers(sc_fit(cs, cs$full), warning = function(w) stop(conditionMessage(w)))
  if (!lavInspect(m0, "converged") || !lavInspect(m1, "converged")) stop(cs$name, ": not converged")
  t0 <- m0@test[[1]]$stat; d0 <- m0@test[[1]]$df
  t1 <- m1@test[[1]]$stat; d1 <- m1@test[[1]]$df
  q <- d0 - d1
  if (!(q > 0)) stop(cs$name, ": df difference ", q)
  doc <- list(name = cs$name, estimator = cs$estimator,
              group = if (is.null(cs$group)) "" else cs$group,
              meanstructure = isTRUE(cs$meanstructure),
              ordered = if (is.null(cs$ordered)) list() else cs$ordered,
              restricted = list(model = cs$restricted$model, group_equal = as.list(cs$restricted$group_equal)),
              full = list(model = cs$full$model, group_equal = as.list(cs$full$group_equal)),
              n = sc_num(lavInspect(m1, "ntotal")),
              restricted_chisq = sc_num(t0), full_chisq = sc_num(t1),
              restricted_df = sc_num(d0), full_df = sc_num(d1), df = sc_num(q))
  if (cs$estimator == "wlsmv") {
    sb0 <- sc_test(m0, "satorra.bentler"); sb1 <- sc_test(m1, "satorra.bentler")
    c0 <- sb0$trace.UGamma / d0; c1 <- sb1$trace.UGamma / d1
    if (abs(c0 - sb0$scaling.factor) > 1e-12 * abs(c0) || abs(c1 - sb1$scaling.factor) > 1e-12 * abs(c1)) stop(cs$name, ": scaling factor is not trace / df")
    cd <- (d0 * c0 - d1 * c1) / q
    stat <- (t0 - t1) / cd
    lrt <- lavTestLRT(m0, m1, method = "satorra.bentler.2001", test = "satorra.bentler")
    doc$restricted_trace_ug <- sc_num(sb0$trace.UGamma)
    doc$full_trace_ug <- sc_num(sb1$trace.UGamma)
    doc$scaling_factor <- sc_num(cd)
  } else {
    stat <- t0 - t1
    lrt <- lavTestLRT(m0, m1)
  }
  p <- pchisq(stat, q, lower.tail = FALSE)
  row <- which(rownames(lrt) == "m0")
  if (abs(lrt[row, "Chisq diff"] - stat) > 1e-9 * max(1, abs(stat)) || lrt[row, "Df diff"] != q) stop(cs$name, ": lavTestLRT disagrees")
  if (abs(lrt[row, "Pr(>Chisq)"] - p) > 1e-9 * max(p, 1e-300) + 1e-15) stop(cs$name, ": lavTestLRT p disagrees")
  doc$statistic <- sc_num(stat)
  doc$p_value <- sc_num(p)
  doc
})

write_golden("sem_compare", list(
  description = "Nested comparison of two SEM fits: lavaan 0.7.3 lavTestLRT — the chi-square difference (ML, DWLS) and the Satorra-Bentler (2001) scaled difference (WLSMV) on the brand survey",
  packages = list(R = R.version.string, lavaan = as.character(packageVersion("lavaan")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  cases = sc_out))
