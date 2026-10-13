# gen_std_residual.R — R oracle for Pulse's OVERLAY_STD_RESIDUAL (U28 E1-S2).
#
# Writes <out_dir>/std_residual.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_std_residual.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite only — no new package pin.
#
# Per case, over an r x c contingency table x:
#   * expected = chisq.test(x)$expected      (row total x column total / N)
#   * stdres   = chisq.test(x)$stdres        (Haberman adjusted residual
#                (O - E) / sqrt(E (1 - r/N) (1 - c/N)))
#   * p        = 2 * pnorm(-|stdres|)        (two-sided normal)
#   * p_row_holm / p_row_bh: p.adjust(p[i, ], method) per row — the
#     `row` multiplicity family; NaN cells are excluded from the family
#     (p.adjust's n counts the non-NA p-values only).
# NaN (a zero-expected cell, or a row / column holding the whole table)
# is written as JSON null.
#
# Cases: Agresti's party identification by gender table (An
# Introduction to Categorical Data Analysis, 2nd ed., 2007, Table 2.5;
# published adjusted residuals 4.50 0.70 -5.32 / -4.50 -0.70 5.32), a
# seeded 4 x 3 Poisson table, a 3 x 3 table with an all-zero row, and
# the weighted 2 x 3 record fixture of
# internal/processing/overlay_weighted_contingency_test.go (ctFixture):
# under kind frequency the Sigma-w table as is, under kind probability
# that table scaled to its Kish n_eff (c = n_eff / Sigma w) before the
# ordinary recurrence — the OVERLAY_CHISQ_MATRIX weighting rule.

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

# sr_num renders one figure: 17 significant digits, NaN as null.
sr_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
sr_mat <- function(m) lapply(seq_len(nrow(m)), function(i) lapply(m[i, ], sr_num))

sr_case <- function(name, x, note) {
  x <- as.matrix(x)
  dimnames(x) <- NULL
  ct <- suppressWarnings(chisq.test(x, correct = FALSE))
  res <- ct$stdres
  p <- 2 * pnorm(-abs(res))
  adj <- function(method) t(apply(p, 1, function(row) p.adjust(row, method)))
  list(
    name = name,
    note = note,
    observed = sr_mat(x),
    expected = sr_mat(ct$expected),
    stdres = sr_mat(res),
    p = sr_mat(p),
    p_row_holm = sr_mat(adj("holm")),
    p_row_bh = sr_mat(adj("BH"))
  )
}

sr_cases <- list()

sr_cases[[length(sr_cases) + 1]] <- sr_case(
  "agresti_party_id",
  matrix(c(762, 327, 468, 484, 239, 477), nrow = 2, byrow = TRUE),
  "Agresti (2007) Table 2.5: gender (female, male) x party (Democrat, Independent, Republican)."
)

set.seed(2802)
sr_cases[[length(sr_cases) + 1]] <- sr_case(
  "seeded_4x3",
  matrix(rpois(12, lambda = c(30, 12, 45, 8, 22, 19, 40, 9, 14, 27, 33, 6)), nrow = 4, byrow = TRUE),
  "Seeded Poisson counts, set.seed(2802)."
)

sr_cases[[length(sr_cases) + 1]] <- sr_case(
  "zero_row_3x3",
  matrix(c(5, 3, 2, 0, 0, 0, 4, 6, 9), nrow = 3, byrow = TRUE),
  "Row 2 is all zero: its expected counts are 0, so its residuals and p-values are undefined (null)."
)

# ctFixture: (row, column, weight) records, 0-based cells.
ct_rec <- data.frame(
  r = c(0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1),
  c = c(0, 0, 0, 1, 1, 2, 2, 2, 2, 0, 0, 1, 1, 1, 2, 2),
  w = c(1, 9, 2, 3, 1, 8, 1, 1, 2, 2, 2, 10, 1, 1, 1, 3)
)
sw <- matrix(0, 2, 3)
for (k in seq_len(nrow(ct_rec))) {
  sw[ct_rec$r[k] + 1, ct_rec$c[k] + 1] <- sw[ct_rec$r[k] + 1, ct_rec$c[k] + 1] + ct_rec$w[k]
}
n_eff <- sum(ct_rec$w)^2 / sum(ct_rec$w^2)
sr_cases[[length(sr_cases) + 1]] <- sr_case(
  "weighted_frequency",
  sw,
  "ctFixture under kind frequency: the Sigma-w table as is (equal to the rep()-expanded rows)."
)
sr_cases[[length(sr_cases) + 1]] <- sr_case(
  "weighted_probability",
  sw * n_eff / sum(ct_rec$w),
  "ctFixture under kind probability: the Sigma-w table scaled to its Kish n_eff (first-order Kish, not Rao-Scott)."
)

write_golden("std_residual", list(
  generator = "scripts/reference/gen_std_residual.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_STD_RESIDUAL",
  r_function = "chisq.test(x, correct = FALSE)$stdres / $expected; 2 * pnorm(-abs(stdres)); p.adjust per row",
  note = "Haberman adjusted standardized residuals; NaN written as null.",
  cases = sr_cases
))
