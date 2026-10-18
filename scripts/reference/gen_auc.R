# gen_auc.R — R oracle for AGG_AUC (model-workbench E2-S1, #342): the
# exact rank-sum AUC with ties counted as half, its DeLong standard
# error and 95% interval (pROC), and the weighted AUC (a hand-computed
# weighted Mann–Whitney double sum).
#
# Writes <out_dir>/auc.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_auc.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Needs pROC
# 1.19.1 (pinned; refused otherwise) + jsonlite.
#
# Unweighted cases: pROC::roc(response = y, predictor = score,
# levels = c(0, 1), direction = "<") — y = 1 is the case (positive)
# class, a higher score predicts it — then auc(), var(method = "delong")
# and ci.auc(method = "delong", conf.level = 0.95), whose interval pROC
# clips to [0, 1]. pROC answers NA for the DeLong figures when either
# class has a single row; those are written as null. Every unweighted
# AUC is also asserted against the brute-force pair count.
#
# Weighted cases have no pROC oracle (pROC has no weights): the figure is
# the weighted Mann–Whitney double sum
#   sum_ij w_i w_j psi(x_i, y_j) / (sum_i w_i * sum_j w_j)
# over positives i and negatives j, psi = 1 / 0.5 / 0. An integer-weight
# case is additionally asserted equal to pROC's AUC on the rows expanded
# by their weights (frequency-weight equivalence). DeLong is withheld
# under a weight, so weighted cases carry no se / interval.

auc_pinned <- c(pROC = "1.19.1")
for (p in names(auc_pinned)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != auc_pinned[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, auc_pinned[[p]]))
  }
}
suppressPackageStartupMessages({
  library(jsonlite)
  library(pROC)
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

auc_num <- function(x) {
  if (length(x) != 1) stop("auc_num() takes a scalar")
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
auc_vec <- function(v) lapply(unname(as.numeric(v)), auc_num)

# auc_pairs is the brute-force weighted Mann–Whitney double sum.
auc_pairs <- function(score, y, w) {
  xp <- score[y == 1]; wp <- w[y == 1]
  xn <- score[y == 0]; wn <- w[y == 0]
  num <- 0
  for (i in seq_along(xp)) {
    psi <- ifelse(xp[i] > xn, 1, ifelse(xp[i] == xn, 0.5, 0))
    num <- num + wp[i] * sum(wn * psi)
  }
  num / (sum(wp) * sum(wn))
}

auc_cases <- list()

auc_case <- function(name, score, y, w = NULL) {
  stopifnot(length(score) == length(y), all(y %in% c(0, 1)))
  out <- list(name = name, score = auc_vec(score), outcome = auc_vec(y),
              n_pos = sum(y == 1), n_neg = sum(y == 0))
  if (is.null(w)) {
    r <- roc(response = y, predictor = score, levels = c(0, 1), direction = "<", quiet = TRUE)
    a <- as.numeric(auc(r))
    brute <- auc_pairs(score, y, rep(1, length(y)))
    if (abs(a - brute) > 1e-12) stop(name, ": pROC auc ", a, " vs pair count ", brute)
    v <- suppressWarnings(var(r, method = "delong"))
    ci <- suppressWarnings(as.numeric(ci.auc(r, method = "delong", conf.level = 0.95)))
    out$weighted <- FALSE
    out$auc <- auc_num(a)
    out$se <- auc_num(sqrt(v))
    out$ci_lower <- auc_num(ci[1])
    out$ci_upper <- auc_num(ci[3])
    if (grepl("^clipped_", name)) {
      z <- qnorm(0.975)
      raw <- c(a - z * sqrt(v), a + z * sqrt(v))
      if (!(raw[1] < 0 || raw[2] > 1)) stop(name, ": the raw interval does not leave [0, 1]")
    }
  } else {
    stopifnot(length(w) == length(y), all(w > 0))
    a <- auc_pairs(score, y, w)
    if (all(w == round(w))) {
      idx <- rep(seq_along(y), w)
      r <- roc(response = y[idx], predictor = score[idx], levels = c(0, 1), direction = "<", quiet = TRUE)
      if (abs(as.numeric(auc(r)) - a) > 1e-12) stop(name, ": weighted pair sum vs expanded pROC")
    }
    out$weighted <- TRUE
    out$weight <- auc_vec(w)
    out$auc <- auc_num(a)
  }
  auc_cases[[length(auc_cases) + 1]] <<- out
}

# Ties within and across classes, small enough to check by hand.
auc_case("ties_small",
  score = c(0.1, 0.4, 0.4, 0.35, 0.8, 0.7, 0.4, 0.2, 0.9, 0.35, 0.6, 0.1),
  y     = c(0,   0,   1,   0,    1,   1,   0,   0,   1,   1,    0,   1))

# 300 rows of a logistic-ish score rounded to 2 places (many ties).
set.seed(20261017)
auc_x <- rnorm(300)
auc_y <- as.numeric(runif(300) < plogis(-0.4 + 1.3 * auc_x))
auc_s <- round(plogis(0.2 + 0.9 * auc_x + rnorm(300, sd = 0.8)), 2)
auc_case("logistic_300", score = auc_s, y = auc_y)

# Perfect separation: AUC 1, DeLong se 0, interval [1, 1].
auc_case("perfect", score = c(1, 2, 3, 4, 5, 6, 7, 8), y = c(0, 0, 0, 0, 1, 1, 1, 1))

# A score that ranks backwards: AUC below 0.5, interval clipped at 0.
auc_case("reversed", score = c(9, 8, 7, 6, 1, 2, 3, 4, 5, 2.5), y = c(1, 1, 1, 0, 0, 0, 0, 0, 1, 0))

# Small, near-perfect and near-reversed: the raw interval runs past 1
# (and below 0), so pROC's clip to [0, 1] is exercised on both ends.
auc_case("clipped_upper", score = c(1, 2, 3, 5, 4, 6), y = c(0, 0, 0, 0, 1, 1))
auc_case("clipped_lower", score = c(6, 5, 4, 2, 3, 1), y = c(0, 0, 0, 0, 1, 1))

# One positive row: pROC's DeLong figures are NA (null).
auc_case("single_positive", score = c(0.3, 0.1, 0.5, 0.2, 0.9), y = c(0, 0, 0, 0, 1))

# Weighted, fractional weights and ties: the hand-computed double sum.
auc_case("weighted_fractional",
  score = c(0.1, 0.4, 0.4, 0.35, 0.8, 0.7, 0.4, 0.2, 0.9, 0.35, 0.6, 0.1),
  y     = c(0,   0,   1,   0,    1,   1,   0,   0,   1,   1,    0,   1),
  w     = c(1.5, 0.25, 2, 1, 0.75, 3, 1.25, 0.5, 2.5, 1, 0.8, 1.2))

# Weighted, integer weights: equals pROC on the expanded rows.
auc_case("weighted_integer",
  score = c(3, 1, 4, 1, 5, 9, 2, 6, 5, 3),
  y     = c(1, 0, 1, 0, 1, 1, 0, 0, 0, 1),
  w     = c(2, 1, 3, 4, 1, 2, 5, 1, 2, 3))

auc_doc <- list(
  generator = "scripts/reference/gen_auc.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(pROC = as.character(packageVersion("pROC")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "AGG_AUC: exact rank-sum AUC (ties half), DeLong se / 95% interval, weighted Mann-Whitney",
  r_function = "pROC::roc(response = y, predictor = score, levels = c(0, 1), direction = '<'); auc(); var(method = 'delong'); ci.auc(method = 'delong', conf.level = 0.95)",
  note = paste(
    "Unweighted: pROC, asserted against the brute-force pair count. se / ci_* null where pROC answers NA (a class with one row); pROC clips the interval to [0, 1].",
    "Weighted: the weighted Mann-Whitney double sum (no pROC weights); an integer-weight case is asserted equal to pROC on the expanded rows. No se / interval under a weight."),
  cases = auc_cases)
write_golden("auc", auc_doc)
