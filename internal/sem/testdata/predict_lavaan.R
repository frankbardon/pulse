# Fit a model with lavaan (ML, expected information, fixed.x) and dump
# its factor scores (lavPredict, method "regression" and "Bartlett")
# as JSON — the oracle for ATTR_SEM_SCORE:
#   Rscript predict_lavaan.R data.csv model.txt meanstructure [group] [group_equal] [stride] [ordered] [estimator] [tight]
# With ordered (comma-separated ordered indicators) the fit is lavaan's
# ordinal estimator (default WLSMV, delta parameterization; meanstructure
# is lavaan's, always TRUE) and the scores are lavaan's empirical Bayes
# modal scores (lavPredict's default for categorical data, the same
# figures as method = "regression", checked here): "ebm" is lavaan's EBM
# routine (lav_predict_eta_ebm_ml) with its BFGS stop tightened from
# reltol 1e-8 to 1e-15 — the mode itself, Pulse's oracle — and
# "ebm_default" lavPredict's own output (up to ~4e-4 off the mode). No
# Bartlett scores are recorded for an ordinal fit. tight = TRUE fits the
# ordinal model by BFGS at reltol 1e-14 instead of lavaan's default
# nlminb stop (which sits ~1e-5 relative off the optimum on the
# multi-group observed-mediator fit; recorded as options$tight_fit).
# group names the data column of a joint multi-group fit (levels in
# sorted order); group_equal is comma-separated (empty: none). Every
# stride-th data row (0-based row index, default 4) is recorded, with
# both methods' scores per latent (lavNames "lv" order); the column
# means over every row of each group are recorded too.
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
desc <- paste(readLines(args[2]), collapse = "\n")
ms <- as.logical(args[3])
grp <- if (length(args) >= 4 && nzchar(args[4])) args[4] else NULL
geq <- if (length(args) >= 5 && nzchar(args[5])) strsplit(args[5], ",")[[1]] else character(0)
stride <- if (length(args) >= 6 && nzchar(args[6])) as.integer(args[6]) else 4L
ord <- if (length(args) >= 7 && nzchar(args[7])) strsplit(args[7], ",")[[1]] else character(0)
est <- if (length(args) >= 8 && nzchar(args[8])) args[8] else "WLSMV"
tightfit <- length(args) >= 9 && isTRUE(as.logical(args[9]))
if (!is.null(grp)) data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))
if (length(ord)) {
  if (tightfit) {
    fit <- sem(desc, data = data, ordered = ord, estimator = est, parameterization = "delta",
               group = grp, group.equal = if (length(geq)) geq else "",
               optim.method = "BFGS", control = list(reltol = 1e-14, maxit = 100000))
  } else {
    fit <- sem(desc, data = data, ordered = ord, estimator = est, parameterization = "delta",
               group = grp, group.equal = if (length(geq)) geq else "")
  }
  ebm_default <- lavPredict(fit)
  regm <- lavPredict(fit, method = "regression")
  if (is.null(grp)) { ebm_default <- list(ebm_default); regm <- list(regm) }
  stopifnot(isTRUE(all.equal(ebm_default, regm, tolerance = 0)))
  src <- deparse(lavaan:::lav_predict_eta_ebm_ml)
  src <- gsub("reltol = 1e-08", "reltol = 1e-15, maxit = 10000", src, fixed = TRUE)
  tight <- eval(parse(text = src))
  environment(tight) <- asNamespace("lavaan")
  nlat <- ncol(ebm_default[[1]])
  reg <- lapply(tight(lavobject = fit), function(m) m[, seq_len(nlat), drop = FALSE])
  bart <- NULL
} else {
  fit <- sem(desc, data = data, meanstructure = ms, estimator = "ML",
             likelihood = "normal", information = "expected", se = "standard",
             fixed.x = TRUE, group = grp, group.equal = if (length(geq)) geq else "")
  reg <- lavPredict(fit, method = "regression")
  bart <- lavPredict(fit, method = "Bartlett")
  if (is.null(grp)) { reg <- list(reg); bart <- list(bart) }
}
labels <- if (is.null(grp)) "" else lavInspect(fit, "group.label")
rows <- function(m) unname(lapply(seq_len(nrow(m)), function(i) unname(m[i, ])))
groups <- lapply(seq_along(labels), function(g) {
  idx <- if (is.null(grp)) seq_len(nrow(data)) else which(data[[grp]] == labels[g])
  keep <- which(((idx - 1L) %% stride) == 0L)
  out <- list(label = labels[g], n = length(idx),
       rows = I(idx[keep] - 1L),
       regression = rows(reg[[g]][keep, , drop = FALSE]),
       regression_mean = unname(colMeans(reg[[g]])))
  if (is.null(bart)) {
    out$ebm_default <- rows(ebm_default[[g]][keep, , drop = FALSE])
  } else {
    out$bartlett <- rows(bart[[g]][keep, , drop = FALSE])
    out$bartlett_mean <- unname(colMeans(bart[[g]]))
  }
  out
})
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = if (length(ord)) c(list(estimator = est, parameterization = "delta", ordered = I(ord), method = "EBM"),
                               if (tightfit) list(tight_fit = TRUE))
            else list(estimator = "ML", likelihood = "normal", information = "expected",
                      fixed.x = TRUE, meanstructure = ms),
  converged = lavInspect(fit, "converged"),
  latents = colnames(if (length(ord)) ebm_default[[1]] else reg[[1]]),
  stride = stride,
  groups = groups)
if (!is.null(grp)) {
  out$options$group <- grp
  if (length(geq)) out$options$group_equal <- I(geq)
}
cat(toJSON(out, auto_unbox = TRUE, digits = NA, pretty = FALSE, na = "null", null = "null"), "\n")
