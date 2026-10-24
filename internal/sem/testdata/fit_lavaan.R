# Fit a model with lavaan (ML, expected information, standard SEs) and
# dump the parameter table, fit measures and moments as JSON:
#   Rscript fit_lavaan.R data.csv model.txt [meanstructure]
# meanstructure is TRUE or FALSE (default TRUE); explicit `~ 1` lines
# switch lavaan's mean structure on whatever it is.
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
desc <- paste(readLines(args[2]), collapse = "\n")
ms <- if (length(args) >= 3) as.logical(args[3]) else TRUE
fit <- sem(desc, data = data, meanstructure = ms, estimator = "ML",
           likelihood = "normal", information = "expected", se = "standard",
           fixed.x = TRUE)
pe <- parameterEstimates(fit, standardized = TRUE, ci = TRUE, level = 0.95)
pt <- parTable(fit)
free <- pt$free[match(paste(pe$lhs, pe$op, pe$rhs), paste(pt$lhs, pt$op, pt$rhs))] > 0
fm <- fitMeasures(fit)
imp <- fitted(fit)
ss <- lavInspect(fit, "sampstat")
na <- function(x) ifelse(is.finite(x), x, NA)
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = "ML", likelihood = "normal", information = "expected",
                 se = "standard", fixed.x = TRUE, meanstructure = ms),
  n = lavInspect(fit, "nobs"),
  converged = lavInspect(fit, "converged"),
  iterations = lavInspect(fit, "iterations"),
  observed = rownames(imp$cov),
  params = lapply(seq_len(nrow(pe)), function(i) list(
    lhs = pe$lhs[i], op = pe$op[i], rhs = pe$rhs[i], label = pe$label[i], free = free[i],
    est = na(pe$est[i]), se = na(pe$se[i]), z = na(pe$z[i]), p = na(pe$pvalue[i]),
    ci_lower = na(pe$ci.lower[i]), ci_upper = na(pe$ci.upper[i]), std_all = na(pe$std.all[i]))),
  fit = as.list(sapply(fm, na)),
  implied_cov = unname(lapply(seq_len(nrow(imp$cov)), function(i) unname(imp$cov[i, ]))),
  implied_mean = if (is.null(imp$mean)) NULL else unname(as.numeric(imp$mean)),
  sample_cov = unname(lapply(seq_len(nrow(ss$cov)), function(i) unname(ss$cov[i, ]))),
  sample_mean = if (is.null(ss$mean)) NULL else unname(as.numeric(ss$mean))
)
cat(toJSON(out, auto_unbox = TRUE, digits = NA, pretty = TRUE, na = "null", null = "null"), "\n")
