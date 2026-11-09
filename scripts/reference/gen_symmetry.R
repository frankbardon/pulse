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
# Exact cases (exact = TRUE, U66 E6-S1): the exact binomial McNemar
# on a 2 x 2, binom.test(n_12, n_12 + n_21)$p.value (p = 0.5, R's
# relErr = 1 + 1e-7 tie rule); statistic = n_12 (binom.test's
# "number of successes"); df is null (no chi-square), continuity
# correction never applies. With n_12 + n_21 = 0 binom.test refuses, so
# the statistic and p are null (Pulse's "nothing to test").
#
# Exact cases also carry the discordant odds ratio n_12 / n_21 and its
# exact conditional CI (U66 E6-S2): binom.test(n_12, n)$conf.int (the
# Clopper-Pearson interval on n_12 / n at 95%) transformed to the odds
# p / (1 - p). n_21 = 0 makes the ratio and the upper bound +Inf
# (written "Inf"); n_12 = 0 makes the ratio and the lower bound 0.
#
# Also writes <out_dir>/binomial.json: the primitive
# statdist.BinomialTwoSidedP against binom.test(x, n, p)$p.value over a
# grid that reaches the relErr tie rule (p = (i + 1) / (n + 1) makes
# d(i) = d(i + 1) on both sides of the mean, equal only up to rounding —
# without the 1 + 1e-7 tolerance n = 2, x = 1, p = 1/3 gives 5/9, not
# 1), x on the mean (p = 1), both
# tails, p = 0 / 1 edges and a large n; each row also carries
# binom.test's conf.int at its level (0.95 unless set) transformed to
# odds (statdist.BinomialOddsInterval).
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
# sy_odds encodes an odds figure: NaN -> null, +Inf -> the string "Inf"
# (Pulse emits +Inf in Go and null on the wire; the string keeps it
# distinct from NaN in the golden), finite -> a number.
sy_odds <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (is.infinite(x) && x > 0) return("Inf")
  sy_num(x)
}
# sy_ci_odds is binom.test's exact (Clopper-Pearson) conf.int on the
# success probability, transformed to the odds p / (1 - p): 0 at a
# 0 lower bound, Inf at a 1 upper bound.
sy_ci_odds <- function(ci) lapply(ci, function(p) sy_odds(p / (1 - p)))
sy_mat <- function(m) lapply(seq_len(nrow(m)), function(i) lapply(as.numeric(m[i, ]), sy_num))

sy_case <- function(name, states, counts, note, correct = TRUE, exact = FALSE) {
  k <- length(states)
  x <- matrix(counts, nrow = k, byrow = TRUE, dimnames = list(states, states))
  up <- upper.tri(x)
  s <- (x + t(x))[up]
  keep <- s > 0
  if (exact) {
    stopifnot(k == 2)
    n12 <- x[1, 2]
    n <- x[1, 2] + x[2, 1]
    stat <- if (n > 0) n12 else NaN
    p <- if (n > 0) binom.test(n12, n)$p.value else NaN
    ci <- if (n > 0) sy_ci_odds(binom.test(n12, n)$conf.int) else list(sy_odds(NaN), sy_odds(NaN))
    return(list(
      name = name,
      note = note,
      states = states,
      counts = sy_mat(x),
      correct = correct,
      exact = TRUE,
      statistic = sy_num(stat),
      df = sy_num(NA),
      p_value = sy_num(p),
      r_statistic = sy_num(if (n > 0) unname(binom.test(n12, n)$statistic) else NaN),
      discordant = sy_num(n),
      pairs_dropped = sy_num(if (n > 0) 0 else 1),
      continuity_corrected = sy_num(0),
      odds_ratio = sy_odds(if (n > 0) n12 / (n - n12) else NaN),
      odds_ratio_ci_lower = ci[[1]],
      odds_ratio_ci_upper = ci[[2]]
    ))
  }
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

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "agresti_approval_exact",
  c("approve", "disapprove"),
  c(794, 150, 86, 570),
  "Agresti (2007) Table 8.1 exact: binom.test(150, 236); correct is inert under exact.",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "tiny_exact",
  c("yes", "no"),
  c(10, 1, 4, 12),
  "Tiny discordant count: binom.test(1, 5) = 0.375, where the chi-square would warn PULSE_OVERLAY_EXPECTED_LOW.",
  correct = FALSE,
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "upper_tail_exact",
  c("before", "after"),
  c(3, 9, 2, 6),
  "n_12 > n_21: the upper branch of binom.test's two-sided rule, binom.test(9, 11).",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "one_way_exact",
  c("yes", "no"),
  c(5, 0, 7, 5),
  "Every move goes one way: binom.test(0, 7) = 2 * 0.5^7; odds ratio 0, its CI lower bound 0.",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "one_way_up_exact",
  c("yes", "no"),
  c(5, 7, 0, 5),
  "Every move goes from the first state to the second: n_21 = 0, so the odds ratio and its CI upper bound are +Inf (null on the wire).",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "symmetric_exact",
  c("yes", "no"),
  c(30, 9, 9, 40),
  "n_12 = n_21 (x on the mean): binom.test's p is exactly 1.",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "odd_tie_exact",
  c("yes", "no"),
  c(4, 6, 7, 2),
  "Odd n = 13, x = 6 next to the centre: the mirror density d(7) = d(6) is matched by the relErr tie rule, p = 1.",
  exact = TRUE
)

sy_cases[[length(sy_cases) + 1]] <- sy_case(
  "diagonal_exact",
  c("yes", "no"),
  c(30, 0, 0, 40),
  "No discordant unit: binom.test(0, 0) refuses, so the statistic and p are null.",
  exact = TRUE
)

write_golden("symmetry", list(
  generator = "scripts/reference/gen_symmetry.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_SYMMETRY",
  r_function = "stats::mcnemar.test (McNemar 2 x 2, Bowker k x k); empty pairs dropped by hand; stats::binom.test (exact 2 x 2)",
  cases = sy_cases
))

bn_case <- function(x, n, p, level = 0.95) {
  r <- binom.test(x, n, p, conf.level = level)
  ci <- sy_ci_odds(r$conf.int)
  list(x = x, n = n, p = sy_num(p), p_value = sy_num(r$p.value),
       level = sy_num(level), odds_lower = ci[[1]], odds_upper = ci[[2]])
}
bn_grid <- list(
  c(150, 236, 0.5), c(1, 5, 0.5), c(9, 11, 0.5), c(0, 7, 0.5), c(7, 7, 0.5),
  c(6, 13, 0.5), c(7, 13, 0.5), c(500, 1000, 0.5), c(470, 1000, 0.5),
  c(49500, 100000, 0.5), c(0, 2, 1 / 3), c(1, 5, 1 / 3), c(3, 5, 1 / 3),
  c(3, 10, 0.3), c(2, 10, 0.25), c(7, 20, 0.2), c(1, 30, 0.1), c(12, 30, 0.1),
  c(0, 4, 0), c(1, 4, 0), c(4, 4, 1), c(3, 4, 1), c(1, 3, 0.25), c(60, 80, 0.9),
  c(1, 2, 1 / 3), c(0, 3, 0.25), c(1, 4, 0.4), c(3, 6, 3 / 7), c(5, 6, 6 / 7),
  # Interval-only rows at other confidence levels and in the far tails
  # (the p-value column rides along): statdist.BinomialOddsInterval.
  c(3, 10, 0.5, 0.9), c(150, 236, 0.5, 0.99), c(0, 7, 0.5, 0.8),
  c(49500, 100000, 0.5, 0.99), c(1, 1000, 0.5, 0.95), c(999, 1000, 0.5, 0.95),
  c(1, 1, 0.5, 0.95), c(2, 3, 0.5, 0.5)
)
write_golden("binomial", list(
  generator = "scripts/reference/gen_symmetry.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "statdist.BinomialTwoSidedP",
  r_function = "stats::binom.test(x, n, p, conf.level = level): $p.value (two-sided, relErr = 1 + 1e-7) and $conf.int transformed to odds p / (1 - p)",
  cases = lapply(bn_grid, function(g) bn_case(g[[1]], g[[2]], g[[3]], if (length(g) > 3) g[[4]] else 0.95))
))
