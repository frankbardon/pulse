# Fit a model with lavaan (ML, expected information, standard SEs) and
# dump the parameter table, fit measures and moments as JSON:
#   Rscript fit_lavaan.R data.csv model.txt [meanstructure] [group] [group_equal] [group_partial] [level]
# meanstructure is TRUE or FALSE (default TRUE); explicit `~ 1` lines
# switch lavaan's mean structure on whatever it is. group names the
# data column of a joint multi-group fit (lavaan's group =; levels in
# sorted order): every parameter row then carries its group label, and
# the moments, n and chi-square are per group (lists in level order).
# Without group the output is the single-group shape. group_equal and
# group_partial are comma-separated lavaan group.equal / group.partial
# values (empty: none), recorded in options. level is the Wald
# interval's level (lavaan's level; default 0.95), recorded in options
# when it is not the default.
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
desc <- paste(readLines(args[2]), collapse = "\n")
ms <- if (length(args) >= 3) as.logical(args[3]) else TRUE
grp <- if (length(args) >= 4 && nzchar(args[4])) args[4] else NULL
argList <- function(i) if (length(args) >= i && nzchar(args[i])) strsplit(args[i], ",")[[1]] else character(0)
geq <- argList(5)
gpart <- argList(6)
level <- if (length(args) >= 7 && nzchar(args[7])) as.numeric(args[7]) else 0.95
if (!is.null(grp)) data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))
fit <- sem(desc, data = data, meanstructure = ms, estimator = "ML",
           likelihood = "normal", information = "expected", se = "standard",
           fixed.x = TRUE, group = grp,
           group.equal = if (length(geq)) geq else "", group.partial = if (length(gpart)) gpart else "")
pe <- parameterEstimates(fit, standardized = TRUE, ci = TRUE, level = level)
pt <- parTable(fit)
if (is.null(grp)) {
  free <- pt$free[match(paste(pe$lhs, pe$op, pe$rhs), paste(pt$lhs, pt$op, pt$rhs))] > 0
  # (a := row is never free: lavaan's free 0)
} else {
  free <- pt$free[match(paste(pe$lhs, pe$op, pe$rhs, pe$group), paste(pt$lhs, pt$op, pt$rhs, pt$group))] > 0
}
fm <- fitMeasures(fit)
imp <- fitted(fit)
ss <- lavInspect(fit, "sampstat")
na <- function(x) ifelse(is.finite(x), x, NA)
rows <- function(m) unname(lapply(seq_len(nrow(m)), function(i) unname(m[i, ])))
param <- function(i) {
  p <- list(
    lhs = pe$lhs[i], op = pe$op[i], rhs = pe$rhs[i], label = pe$label[i], free = free[i],
    est = na(pe$est[i]), se = na(pe$se[i]), z = na(pe$z[i]), p = na(pe$pvalue[i]),
    ci_lower = na(pe$ci.lower[i]), ci_upper = na(pe$ci.upper[i]), std_all = na(pe$std.all[i]))
  # a := row belongs to no group (lavaan's group 0): null
  if (!is.null(grp)) p$group <- if (pe$group[i] > 0) lavInspect(fit, "group.label")[pe$group[i]] else NA
  p
}
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = "ML", likelihood = "normal", information = "expected",
                 se = "standard", fixed.x = TRUE, meanstructure = ms),
  n = lavInspect(fit, "nobs"),
  converged = lavInspect(fit, "converged"),
  iterations = lavInspect(fit, "iterations"))
if (level != 0.95) out$options$level <- level
if (is.null(grp)) {
  out$observed <- rownames(imp$cov)
  out$params <- lapply(seq_len(nrow(pe)), param)
  out$fit <- as.list(sapply(fm, na))
  out$implied_cov <- rows(imp$cov)
  out$implied_mean <- if (is.null(imp$mean)) NULL else unname(as.numeric(imp$mean))
  out$sample_cov <- rows(ss$cov)
  out$sample_mean <- if (is.null(ss$mean)) NULL else unname(as.numeric(ss$mean))
} else {
  labels <- lavInspect(fit, "group.label")
  out$options$group <- grp
  if (length(geq)) out$options$group_equal <- I(geq)
  if (length(gpart)) out$options$group_partial <- I(gpart)
  out$group_labels <- labels
  out$n_total <- sum(lavInspect(fit, "nobs"))
  out$observed <- rownames(imp[[1]]$cov)
  out$params <- lapply(seq_len(nrow(pe)), param)
  out$fit <- as.list(sapply(fm, na))
  out$chi2_group <- unname(as.numeric(lavInspect(fit, "test")$standard$stat.group))
  out$groups <- unname(lapply(seq_along(labels), function(g) list(
    label = labels[g],
    implied_cov = rows(imp[[g]]$cov),
    implied_mean = unname(as.numeric(imp[[g]]$mean)),
    sample_cov = rows(ss[[g]]$cov),
    sample_mean = unname(as.numeric(ss[[g]]$mean)))))
}
cat(toJSON(out, auto_unbox = TRUE, digits = NA, pretty = TRUE, na = "null", null = "null"), "\n")
