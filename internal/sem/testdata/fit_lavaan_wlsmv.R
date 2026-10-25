# Fit a model with lavaan's ordinal estimators (DWLS or WLSMV, delta
# parameterization) and dump the parameter table, the fit measures and
# every intermediate of the estimator as JSON, for the TestWLSMV_* parity
# tests:
#   Rscript fit_lavaan_wlsmv.R data.csv model.txt ordered estimator
# ordered is the comma-separated ordered columns (the model's other
# observed variables are numeric); estimator DWLS or WLSMV. Besides the
# estimates and fit measures it records lavaan's WLS.obs, WLS.est (at
# the estimate), the diagonal weight WLS.V, Gamma (NACOV), the Jacobian
# Delta (rows the WLS.obs statistics, columns the free parameters, both
# named), the free parameters' covariance (vcov) and the scaled-shifted
# test's trace(U Gamma) and trace((U Gamma)^2).
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
desc <- paste(readLines(args[2]), collapse = "\n")
ord <- strsplit(args[3], ",")[[1]]
est <- args[4]
fit <- sem(desc, data = data, ordered = ord, estimator = est, parameterization = "delta")
pe <- parameterEstimates(fit, standardized = TRUE, ci = TRUE)
pt <- parTable(fit)
key <- function(l, o, r) paste(l, o, r)
free <- pt$free[match(key(pe$lhs, pe$op, pe$rhs), key(pt$lhs, pt$op, pt$rhs))] > 0
na <- function(x) ifelse(is.finite(x), x, NA)
rows <- function(m) unname(lapply(seq_len(nrow(m)), function(i) unname(m[i, ])))
param <- function(i) list(
  lhs = pe$lhs[i], op = pe$op[i], rhs = pe$rhs[i], label = pe$label[i], free = isTRUE(free[i]),
  est = na(pe$est[i]), se = na(pe$se[i]), z = na(pe$z[i]), p = na(pe$pvalue[i]),
  ci_lower = na(pe$ci.lower[i]), ci_upper = na(pe$ci.upper[i]), std_all = na(pe$std.all[i]))
fm_names <- c("npar", "fmin", "chisq", "df", "pvalue", "baseline.chisq", "baseline.df", "cfi", "tli",
              "rmsea", "nfi", "gfi_lisrel", "agfi_lisrel", "logl", "aic", "bic", "ntotal")
if (est == "WLSMV") {
  fm_names <- c(fm_names, "chisq.scaled", "df.scaled", "pvalue.scaled", "chisq.scaling.factor",
                "baseline.chisq.scaled", "baseline.chisq.scaling.factor", "cfi.scaled", "tli.scaled", "rmsea.scaled")
}
fm <- fitMeasures(fit, fm_names)
tst <- lavInspect(fit, "test")
fpt <- pt[pt$free > 0, ]
fpt <- fpt[order(fpt$free), ]
dl <- lavInspect(fit, "delta")
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = est, ordered = ord, parameterization = "delta",
                 se = lavInspect(fit, "options")$se, test = I(lavInspect(fit, "options")$test)),
  n = lavInspect(fit, "nobs"),
  converged = lavInspect(fit, "converged"),
  observed = lavNames(fit, "ov"),
  params = lapply(seq_len(nrow(pe)), param),
  fit = as.list(sapply(fm, na)),
  free_params = unname(paste(fpt$lhs, fpt$op, fpt$rhs)),
  wls_names = rownames(dl),
  wls_obs = unname(as.numeric(lavInspect(fit, "wls.obs"))),
  wls_est = unname(as.numeric(lavInspect(fit, "wls.est"))),
  wls_v = unname(diag(lavInspect(fit, "wls.v"))),
  gamma = rows(lavInspect(fit, "gamma")),
  delta = rows(dl),
  vcov = rows(lavInspect(fit, "vcov")))
if (!is.null(tst$scaled.shifted)) {
  ss <- tst$scaled.shifted
  out$scaled_shifted <- list(stat = ss$stat, trace_ugamma = ss$trace.UGamma, trace_ugamma2 = ss$trace.UGamma2,
                             scaling_factor = ss$scaling.factor, shift_parameter = ss$shift.parameter)
}
cat(toJSON(out, auto_unbox = TRUE, digits = I(17), pretty = FALSE, na = "null", null = "null"), "\n")
