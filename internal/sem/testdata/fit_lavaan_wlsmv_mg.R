# The multi-group branch of fit_lavaan_wlsmv.R (sourced by it with data,
# desc, ord, est, grp and geq set): lavaan's DWLS / WLSMV with group =
# grp and group.equal = geq, delta parameterization. Each parameter row
# and free parameter carries its group label; the per-group estimator
# intermediates sit under "groups" in group order.
data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))
fit <- sem(desc, data = data, ordered = ord, estimator = est, parameterization = "delta",
           group = grp, group.equal = if (length(geq)) geq else "")
labels <- lavInspect(fit, "group.label")
pe <- parameterEstimates(fit, standardized = TRUE, ci = TRUE)
pt <- parTable(fit)
key <- function(l, o, r, g) paste(l, o, r, g)
free <- pt$free[match(key(pe$lhs, pe$op, pe$rhs, pe$group), key(pt$lhs, pt$op, pt$rhs, pt$group))] > 0
na <- function(x) ifelse(is.finite(x), x, NA)
rows <- function(m) unname(lapply(seq_len(nrow(m)), function(i) unname(m[i, ])))
param <- function(i) list(
  lhs = pe$lhs[i], op = pe$op[i], rhs = pe$rhs[i], label = pe$label[i], free = isTRUE(free[i]),
  group = if (pe$group[i] > 0) labels[pe$group[i]] else NA,
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
wo <- lavInspect(fit, "wls.obs"); we <- lavInspect(fit, "wls.est"); wv <- lavInspect(fit, "wls.v"); gm <- lavInspect(fit, "gamma")
groups <- lapply(seq_along(labels), function(g) list(
  label = labels[g],
  n = lavInspect(fit, "nobs")[g],
  wls_names = rownames(dl[[g]]),
  wls_obs = unname(as.numeric(wo[[g]])),
  wls_est = unname(as.numeric(we[[g]])),
  wls_v = unname(diag(wv[[g]])),
  gamma = rows(gm[[g]]),
  delta = rows(dl[[g]])))
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = est, ordered = ord, parameterization = "delta", group = grp,
                 group_equal = I(geq),
                 se = lavInspect(fit, "options")$se, test = I(lavInspect(fit, "options")$test)),
  n = sum(lavInspect(fit, "nobs")),
  group_labels = I(labels),
  converged = lavInspect(fit, "converged"),
  observed = lavNames(fit, "ov"),
  params = lapply(seq_len(nrow(pe)), param),
  fit = as.list(sapply(fm, na)),
  free_params = unname(paste(fpt$lhs, fpt$op, fpt$rhs, labels[fpt$group])),
  groups = groups,
  vcov = rows(lavInspect(fit, "vcov")))
if (!is.null(tst$scaled.shifted)) {
  ss <- tst$scaled.shifted
  out$scaled_shifted <- list(stat = ss$stat, trace_ugamma = ss$trace.UGamma, trace_ugamma2 = ss$trace.UGamma2,
                             scaling_factor = ss$scaling.factor, shift_parameter = ss$shift.parameter)
}
cat(toJSON(out, auto_unbox = TRUE, digits = I(17), pretty = FALSE, na = "null", null = "null"), "\n")
