# gen_rake.R — R oracle for Pulse's OVERLAY_RAKE (U28 E2-S3).
#
# Writes <out_dir>/rake.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_rake.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Needs base
# stats (loglin), the survey package (cross-check) and jsonlite.
#
# Per case, over a count table x (rows x columns) and raw row / column
# targets:
#   * targets_as "shares": each axis's targets / their sum * sum(x);
#     "counts": as given (the two totals agree)
#   * fitted: stats::loglin(outer(r, c) / sum(r), list(1, 2), start = x,
#     fit = TRUE) — 2-D iterative proportional fitting of x to the
#     margins r and c (rows adjusted first, then columns, per cycle, as
#     Pulse sweeps). Converged cases run to eps 1e-13 (max absolute
#     margin deviation) within 10,000 cycles; a capped case runs exactly
#     max_iter cycles (eps 0 never stops early) so the fit after that
#     many sweeps is compared as is.
#   * every converged case is cross-checked against survey::rake on the
#     one-row-per-non-zero-cell design weighted by the counts (relative
#     1e-6: survey stops on its own epsilon)
#   * factors: fitted / x, null where x is 0 (a structural zero stays 0)

suppressPackageStartupMessages({
  library(jsonlite)
  library(survey)
})

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

rk_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
rk_mat <- function(m) {
  m <- as.matrix(m)
  lapply(seq_len(nrow(m)), function(i) lapply(as.numeric(m[i, ]), rk_num))
}
rk_named <- function(v) {
  out <- lapply(as.numeric(v), rk_num)
  names(out) <- names(v)
  out
}

rk_survey <- function(x, r, c) {
  rows <- rownames(x)
  cols <- colnames(x)
  df <- expand.grid(row = rows, col = cols, stringsAsFactors = FALSE)
  df$w <- as.numeric(x[cbind(match(df$row, rows), match(df$col, cols))])
  df <- df[df$w > 0, ]
  df$row <- factor(df$row, levels = rows)
  df$col <- factor(df$col, levels = cols)
  des <- svydesign(ids = ~1, weights = ~w, data = df)
  pr <- data.frame(row = factor(rows, levels = rows), Freq = as.numeric(r))
  pc <- data.frame(col = factor(cols, levels = cols), Freq = as.numeric(c))
  rk <- rake(des, list(~row, ~col), list(pr, pc), control = list(maxit = 10000, epsilon = 1e-12, verbose = FALSE))
  tab <- svytable(~ row + col, rk)
  out <- matrix(0, nrow = length(rows), ncol = length(cols), dimnames = list(rows, cols))
  out[rownames(tab), colnames(tab)] <- tab
  out
}

rk_case <- function(name, rows, cols, x, row_targets, col_targets, note,
                    targets_as = "shares", max_iter = 10000, capped = FALSE) {
  x <- matrix(as.numeric(x), nrow = length(rows), byrow = TRUE, dimnames = list(rows, cols))
  names(row_targets) <- rows
  names(col_targets) <- cols
  if (targets_as == "shares") {
    r <- row_targets / sum(row_targets) * sum(x)
    c <- col_targets / sum(col_targets) * sum(x)
  } else {
    stopifnot(isTRUE(all.equal(sum(row_targets), sum(col_targets))))
    r <- row_targets
    c <- col_targets
  }
  target <- outer(r, c) / sum(r)
  dimnames(target) <- dimnames(x)
  fit <- suppressWarnings(loglin(target, list(1, 2), start = x, fit = TRUE,
                                 eps = if (capped) 0 else 1e-13,
                                 iter = max_iter, print = FALSE)$fit)
  if (!capped) {
    stopifnot(max(abs(rowSums(fit) - r) / pmax(r, 1)) < 1e-9)
    stopifnot(max(abs(colSums(fit) - c) / pmax(c, 1)) < 1e-9)
    sv <- rk_survey(x, r, c)
    stopifnot(isTRUE(all.equal(as.numeric(sv), as.numeric(fit), tolerance = 1e-6)))
  }
  factors <- fit / x
  factors[x == 0] <- NA
  list(
    name = name,
    note = note,
    rows = as.list(rows),
    columns = as.list(cols),
    counts = rk_mat(x),
    row_targets = rk_named(row_targets),
    col_targets = rk_named(col_targets),
    targets_as = targets_as,
    max_iter = as.integer(max_iter),
    capped = capped,
    row_margins = rk_named(r),
    col_margins = rk_named(c),
    fitted = rk_mat(fit),
    factors = rk_mat(factors)
  )
}

rk_cases <- list()

rk_cases[[length(rk_cases) + 1]] <- rk_case(
  "age_region_census_shares",
  c("18-34", "35-54", "55+"),
  c("north", "south", "east", "west"),
  c(
    40, 22, 31, 18,
    35, 41, 28, 30,
    20, 37, 15, 33
  ),
  c(0.31, 0.37, 0.32),
  c(0.24, 0.27, 0.22, 0.27),
  "Survey sample age x region raked to census shares (normalized, scaled to the sample total)."
)

rk_cases[[length(rk_cases) + 1]] <- rk_case(
  "count_targets",
  c("a", "b", "c"),
  c("x", "y", "z"),
  c(
    12, 5, 8,
    7, 19, 4,
    3, 6, 21
  ),
  c(100, 150, 250),
  c(220, 130, 150),
  "Counts targets with one grand total (500) against a table of 85.",
  targets_as = "counts"
)

rk_cases[[length(rk_cases) + 1]] <- rk_case(
  "structural_zeros",
  c("r1", "r2", "r3", "r4"),
  c("c1", "c2", "c3", "c4"),
  c(
    30, 0, 12, 7,
    0, 25, 9, 14,
    11, 8, 0, 20,
    6, 13, 17, 0
  ),
  c(0.2, 0.3, 0.25, 0.25),
  c(0.15, 0.3, 0.25, 0.3),
  "A zero on the diagonal of every row: the zeros stay zero and their factors are null."
)

set.seed(2803)
seeded <- matrix(rpois(30, lambda = 25) + 1, nrow = 6)
rk_cases[[length(rk_cases) + 1]] <- rk_case(
  "seeded_6x5",
  paste0("g", 1:6),
  paste0("k", 1:5),
  t(seeded),
  c(5, 9, 14, 20, 26, 26),
  c(30, 25, 20, 15, 10),
  "Seeded 6 x 5 table (Poisson(25) + 1), set.seed(2803), raked to skewed shares."
)

rk_cases[[length(rk_cases) + 1]] <- rk_case(
  "capped_two_sweeps",
  c("18-34", "35-54", "55+"),
  c("north", "south", "east", "west"),
  c(
    40, 22, 31, 18,
    35, 41, 28, 30,
    20, 37, 15, 33
  ),
  c(0.31, 0.37, 0.32),
  c(0.24, 0.27, 0.22, 0.27),
  "The census case stopped after exactly 2 sweeps: not converged, the fit after two cycles.",
  max_iter = 2,
  capped = TRUE
)

write_golden("rake", list(
  generator = "scripts/reference/gen_rake.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    survey = as.character(packageVersion("survey")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_RAKE",
  r_function = "stats::loglin(outer(r, c) / sum(r), list(1, 2), start = x, fit = TRUE); cross-check survey::rake + svytable",
  cases = rk_cases
))
