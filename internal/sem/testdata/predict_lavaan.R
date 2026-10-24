# Fit a model with lavaan (ML, expected information, fixed.x) and dump
# its factor scores (lavPredict, method "regression" and "Bartlett")
# as JSON — the oracle for ATTR_SEM_SCORE:
#   Rscript predict_lavaan.R data.csv model.txt meanstructure [group] [group_equal] [stride]
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
if (!is.null(grp)) data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))
fit <- sem(desc, data = data, meanstructure = ms, estimator = "ML",
           likelihood = "normal", information = "expected", se = "standard",
           fixed.x = TRUE, group = grp, group.equal = if (length(geq)) geq else "")
reg <- lavPredict(fit, method = "regression")
bart <- lavPredict(fit, method = "Bartlett")
if (is.null(grp)) { reg <- list(reg); bart <- list(bart) }
labels <- if (is.null(grp)) "" else lavInspect(fit, "group.label")
rows <- function(m) unname(lapply(seq_len(nrow(m)), function(i) unname(m[i, ])))
groups <- lapply(seq_along(labels), function(g) {
  idx <- if (is.null(grp)) seq_len(nrow(data)) else which(data[[grp]] == labels[g])
  keep <- which(((idx - 1L) %% stride) == 0L)
  list(label = labels[g], n = length(idx),
       rows = I(idx[keep] - 1L),
       regression = rows(reg[[g]][keep, , drop = FALSE]),
       bartlett = rows(bart[[g]][keep, , drop = FALSE]),
       regression_mean = unname(colMeans(reg[[g]])),
       bartlett_mean = unname(colMeans(bart[[g]])))
})
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(estimator = "ML", likelihood = "normal", information = "expected",
                 fixed.x = TRUE, meanstructure = ms),
  converged = lavInspect(fit, "converged"),
  latents = colnames(reg[[1]]),
  stride = stride,
  groups = groups)
if (!is.null(grp)) {
  out$options$group <- grp
  if (length(geq)) out$options$group_equal <- I(geq)
}
cat(toJSON(out, auto_unbox = TRUE, digits = NA, pretty = FALSE, na = "null", null = "null"), "\n")
