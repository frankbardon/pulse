# gen_symmetry.R — R oracle for Pulse's OVERLAY_SYMMETRY (U30 E7-S1).
#
# Writes <out_dir>/symmetry.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_symmetry.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite only — no new package pin.
#
# Per case, over a square table x of from-state (row) x to-state
# (column) counts of paired observations, states aligned by name:
#   * statistic / df / p_value = mcnemar.test(x, correct)$statistic /
#     $parameter / $p.value — McNemar for 2 x 2 (Edwards' continuity
#     correction under correct = TRUE when n_12 != n_21), Bowker for k x k.
#   * When a pair (i, j) has n_ij + n_ji = 0, mcnemar.test keeps it (0/0,
#     so its statistic is NaN, recorded as r_statistic = null) and Pulse
#     drops it: the figures are then the same Bowker sum over the kept
#     pairs, with df = the number of kept pairs, computed here in base R.
#     With no pair kept the statistic and p are NaN (null) and df is 0.
#   * discordant = sum of the off-diagonal counts; pairs_dropped = the
#     empty pairs; continuity_corrected = 1 when the correction applied.
# NaN is written as JSON null.
#
# Cases: Agresti's presidential approval ratings (An Introduction to
# Categorical Data Analysis, 2nd ed., 2007, Table 8.1: 794 / 150 / 86 /
# 570; published McNemar z^2 = 17.36 uncorrected; also R's own
# ?mcnemar.test example) with and without the correction, a 3 x 3
# Bowker table, a seeded 4 x 4 Poisson table, a 3 x 3 table with one
# empty pair, a balanced 2 x 2 (n_12 = n_21: no correction, statistic 0)
# and a diagonal-only 2 x 2 (no pair kept).

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

sy_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
sy_mat <- function(m) lapply(seq_len(nrow(m)), function(i) lapply(as.numeric(m[i, ]), sy_num))

sy_case <- function(name, states, counts, note, correct = TRUE) {
  k <- length(states)
  x <- matrix(counts, nrow = k, byrow = TRUE, dimnames = list(states, states))
  up <- upper.tri(x)
  s <- (x + t(x))[up]
  keep <- s > 0
  r <- suppressWarnings(mcnemar.test(x, correct = correct))
  applied <- correct && k == 2 && any(x - t(x) != 0)
  if (all(keep)) {
    stat <- unname(r$statistic)
    df <- unname(r$parameter)
    p <- r$p.value
  } else {
    d <- abs((x - t(x))[up])
    if (applied) d <- pmax(0, d - 1)
    df <- sum(keep)
    stat <- if (df > 0) sum(d[keep]^2 / s[keep]) else NaN
    p <- if (df > 0) pchisq(stat, df, lower.tail = FALSE) else NaN
  }
  list(
    name = name,
    note = note,
    states = states,
    counts = sy_mat(x),
    correct = correct,
    statistic = sy_num(stat),
    df = sy_num(df),
    p_value = sy_num(p),
    r_statistic = sy_num(unname(r$statistic)),
    discordant = sy_num(sum(s)),
    pairs_dropped = sy_num(sum(!keep)),
    continuity_corrected = sy_num(if (applied) 1 else 0)
  )
}

sy_cases <- list()

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "agresti_approval_corrected",
  c("approve", "disapprove"),
  c(794, 150, 86, 570),
  "Agresti (2007) Table 8.1, first by second survey; correct = TRUE (R's default): (|150 - 86| - 1)^2 / 236.",
  correct = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "agresti_approval_uncorrected",
  c("approve", "disapprove"),
  c(794, 150, 86, 570),
  "Agresti (2007) Table 8.1 with correct = FALSE: the published McNemar z^2 = 64^2 / 236 = 17.36.",
  correct = FALSE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "bowker_3x3",
  c("low", "mid", "high"),
  c(
    40, 12, 4,
    6, 55, 18,
    2, 9, 30
  ),
  "3 x 3 Bowker table, every pair non-empty; correct is inert for k > 2."
)

set.seed(3071)
seeded <- matrix(rpois(16, lambda = 6) + 1, nrow = 4)
diag(seeded) <- diag(seeded) + 25
sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "seeded_4x4",
  paste0("s", 1:4),
  as.numeric(t(seeded)),
  "Seeded 4 x 4 table, set.seed(3071): Poisson(6) + 1 off the diagonal, + 25 on it."
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "empty_pair_3x3",
  c("a", "b", "c"),
  c(
    20, 8, 0,
    3, 25, 7,
    0, 11, 15
  ),
  "The (a, c) pair has no move either way: mcnemar.test's statistic is NaN; Pulse drops the pair, df 2."
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "balanced_2x2",
  c("yes", "no"),
  c(30, 9, 9, 40),
  "n_12 = n_21: no correction applies (R's any(x - t(x) != 0) rule), statistic 0, p 1."
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "diagonal_2x2",
  c("yes", "no"),
  c(30, 0, 0, 40),
  "Nobody changed state: no pair is kept, so the statistic and p are null and df is 0."
)

write_golden("symmetry", list(
  generator = "scripts/reference/gen_symmetry.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_SYMMETRY",
  r_function = "stats::mcnemar.test (McNemar 2 x 2, Bowker k x k); empty pairs dropped by hand",
  cases = sy_cases
))
