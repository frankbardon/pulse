# Bootstrap a model with lavaan (ML, expected information, se =
# "bootstrap", percentile intervals) and dump the parameter table as
# JSON — the distributional reference for MAT_SEM's params.resample
# "bootstrap" (a bootstrap is random: Pulse's and lavaan's draws differ,
# so the parity is a sampling tolerance, never the bits):
#   Rscript fit_lavaan_boot.R data.csv model.txt meanstructure group group_equal iters seed
# group is a data column ("" none: single group; levels sorted, the
# draws within each group), group_equal comma-separated ("" none).
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
desc <- paste(readLines(args[2]), collapse = "\n")
ms <- as.logical(args[3])
grp <- if (nzchar(args[4])) args[4] else NULL
geq <- if (nzchar(args[5])) strsplit(args[5], ",")[[1]] else character(0)
iters <- as.integer(args[6])
seed <- as.integer(args[7])
if (!is.null(grp)) data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))
set.seed(seed)
fit <- sem(desc, data = data, meanstructure = ms, estimator = "ML",
           likelihood = "normal", information = "expected", se = "bootstrap",
           bootstrap = iters, fixed.x = TRUE, group = grp,
           group.equal = if (length(geq)) geq else "")
pe <- parameterEstimates(fit, ci = TRUE, level = 0.95, boot.ci.type = "perc")
pt <- parTable(fit)
key <- if (is.null(grp)) paste(pe$lhs, pe$op, pe$rhs) else paste(pe$lhs, pe$op, pe$rhs, pe$group)
ptkey <- if (is.null(grp)) paste(pt$lhs, pt$op, pt$rhs) else paste(pt$lhs, pt$op, pt$rhs, pt$group)
free <- pt$free[match(key, ptkey)] > 0
na <- function(x) ifelse(is.finite(x), x, NA)
param <- function(i) {
  p <- list(lhs = pe$lhs[i], op = pe$op[i], rhs = pe$rhs[i], label = pe$label[i], free = free[i],
            est = na(pe$est[i]), se = na(pe$se[i]), z = na(pe$z[i]), p = na(pe$pvalue[i]),
            ci_lower = na(pe$ci.lower[i]), ci_upper = na(pe$ci.upper[i]))
  if (!is.null(grp)) p$group <- if (pe$group[i] > 0) lavInspect(fit, "group.label")[pe$group[i]] else NA
  p
}
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = "ML", likelihood = "normal", information = "expected",
                 se = "bootstrap", bootstrap = iters, seed = seed, boot_ci_type = "perc",
                 fixed.x = TRUE, meanstructure = ms),
  n = sum(lavInspect(fit, "nobs")),
  converged = lavInspect(fit, "converged"),
  bootstrap_successful = nrow(lavInspect(fit, "boot")),
  params = lapply(seq_len(nrow(pe)), param))
if (!is.null(grp)) {
  out$options$group <- grp
  if (length(geq)) out$options$group_equal <- I(geq)
}
cat(toJSON(out, auto_unbox = TRUE, digits = NA, pretty = TRUE, na = "null", null = "null"), "\n")
