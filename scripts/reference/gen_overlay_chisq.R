# gen_overlay_chisq.R — R oracle for the unweighted chi-square crosstab
# overlays (U64 E2-S2, #617): OVERLAY_CHISQ_ROW, OVERLAY_CHISQ_COL and
# OVERLAY_CHISQ_MATRIX. Before it they had internal checks only; the R
# pin was the frequency-weighted reference.
#
# Writes <out_dir>/overlay_chisq_axes.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir, write_golden()
# and num(); runnable on its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_overlay_chisq.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite 2.0.0 only; refuses any R but 4.6.1. Deterministic
# tables, no RNG.
#
# Each case is a host crosstab of counts (rows x columns). The figures:
#   * matrix — chisq.test(table, correct = FALSE): the independence
#     statistic, df = (r - 1)(c - 1), p, the smallest expected count.
#     Pulse applies no Yates correction; on a 2 x 2 table chisq.test's
#     default (corrected) figure is kept as r_yates_*.
#   * rows — per row i, chisq.test(table[i, ], p = colSums / N): the
#     row's goodness of fit to the column margin's distribution, df =
#     c - 1. Its expected counts are rowSum_i * colSums / N, the
#     independence table's row i, which is the overlay's definition.
#   * cols — per column j, chisq.test(table[, j], p = rowSums / N), df =
#     r - 1: the same, transposed.
# Every table has a positive total in each row and column (an empty
# margin is the overlay's degenerate arm, pinned by its own tests).

suppressPackageStartupMessages(library(jsonlite))

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
if (!exists("num")) {
  num <- function(x) {
    if (length(x) != 1) stop("num() takes a scalar")
    if (!is.finite(x)) stop("non-finite reference value: ", x)
    structure(sprintf("%.17g", x), class = "json")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_overlay_chisq.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("jsonlite")) != "2.0.0") stop("gen_overlay_chisq.R pins jsonlite 2.0.0, got ", packageVersion("jsonlite"))

oc_fit <- function(x, p) {
  ct <- suppressWarnings(chisq.test(x, p = p))
  list(statistic = num(unname(ct$statistic)), df = num(unname(ct$parameter)),
       p_value = num(ct$p.value), expected_min = num(min(ct$expected)))
}
oc_case <- function(name, tab) {
  if (any(rowSums(tab) == 0) || any(colSums(tab) == 0)) stop(name, ": empty margin")
  N <- sum(tab)
  m <- suppressWarnings(chisq.test(tab, correct = FALSE))
  matrix_fig <- list(statistic = num(unname(m$statistic)), df = num(unname(m$parameter)),
                     p_value = num(m$p.value), expected_min = num(min(m$expected)))
  if (all(dim(tab) == 2)) {
    y <- suppressWarnings(chisq.test(tab))
    matrix_fig$r_yates_statistic <- num(unname(y$statistic))
    matrix_fig$r_yates_p_value <- num(y$p.value)
  }
  list(
    name = name,
    table = lapply(seq_len(nrow(tab)), function(i) as.numeric(tab[i, ])),
    matrix = matrix_fig,
    rows = lapply(seq_len(nrow(tab)), function(i) oc_fit(tab[i, ], colSums(tab) / N)),
    cols = lapply(seq_len(ncol(tab)), function(j) oc_fit(tab[, j], rowSums(tab) / N))
  )
}
oc_cases <- list(
  oc_case("two_by_two", matrix(c(12, 5, 7, 16), 2, byrow = TRUE)),
  oc_case("two_by_three", matrix(c(10, 20, 30, 20, 20, 20), 2, byrow = TRUE)),
  oc_case("three_by_four", matrix(c(10, 14, 8, 20, 12, 9, 15, 11, 6, 18, 13, 7), 3, byrow = TRUE)),
  oc_case("zero_cell_low_expected", matrix(c(0, 4, 9, 6, 3, 2, 1, 7, 5), 3, byrow = TRUE)),
  oc_case("tiny_p", matrix(c(180, 20, 15, 30, 170, 25), 2, byrow = TRUE)),
  oc_case("five_by_two", matrix(c(5, 9, 12, 7, 7, 8, 11, 6, 3, 14), 5, byrow = TRUE)),
  oc_case("independent_like", matrix(c(25, 26, 24, 50, 49, 51, 75, 74, 76), 3, byrow = TRUE))
)
write_golden("overlay_chisq_axes", c(list(
  generator = "scripts/reference/gen_overlay_chisq.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_CHISQ_ROW, OVERLAY_CHISQ_COL, OVERLAY_CHISQ_MATRIX",
  r_function = "chisq.test(table, correct = FALSE); chisq.test(table[i, ], p = colSums / N); chisq.test(table[, j], p = rowSums / N)",
  note = "Unweighted count hosts; Pulse applies no Yates correction (the corrected 2 x 2 matrix figure is kept as r_yates_*)."
), list(cases = oc_cases)))
