# gen_compare_resample.R — R oracle for the Wald nested-model comparison
# over a resampled fit (Compose `compare` `test: wald` beside `resample`,
# U69 #642).
#
# Every case is a MANUAL replicate loop in base R over the FULL model
# (lm / glm): each replicate refits it on its rows and keeps its
# coefficients, and the replicate covariance is
#
#   jackknife:  V = (g - 1)/g * sum_k (b_(k) - bbar)(b_(k) - bbar)'
#   bootstrap:  V = cov(b_(b)) (the B - 1 divisor)
#
# — the covariance the resampled coefficient table's standard errors are
# the square roots of. The Wald statistic of the full model's extra terms
# S (the restricted model is the full one without them) is then
# car::linearHypothesis(m1, "<term> = 0" for each S, vcov. = V,
# test = "Chisq") — b_S' V_SS^-1 b_S on chi-square(q), checked equal to
# the hand-computed form at 1e-12 relative.
#
# Jackknife cases are leave-one-out (every row its own group) and one
# delete-a-cluster case (jackknife_by = g, 12 clusters). The bootstrap's
# draws are R's own: each case records its B index sets (samples, 0-based
# rows of `rows`), drawn with sample.int under the script's seed, and the
# Go test runs Pulse's replicate hook and replicate summary over exactly
# those sets — Pulse's own PCG draws are pinned seed-stable by
# same-process tests instead.
#
# Writes <out_dir>/compare_resample.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_compare_resample.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned: R 4.6.1, car 3.1-5, base stats + jsonlite. glm fits use
# glm.control(epsilon = 1e-14, maxit = 200); a replicate that warns
# (separation, no convergence) stops the script, so no fixture pins a
# replicate Pulse would refuse. Seeded (set.seed(20261125)); standalone
# and sourced last, so no other golden moves. Rows are [x1, x2, x3, y, g]
# (g the cluster id).

suppressPackageStartupMessages({
  library(jsonlite)
  library(car)
})

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_compare_resample.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("car")) != "3.1.5") stop("gen_compare_resample.R pins car 3.1-5, got ", packageVersion("car"))

cr_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
cr_num <- function(x) {
  if (!is.finite(x)) stop("non-finite reference value")
  structure(sprintf("%.17g", x), class = "json")
}
cr_int <- function(v) structure(paste0("[", paste(as.integer(v), collapse = ","), "]"), class = "json")

cr_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# cr_fit fits the full model (y on full) on d, stopping on any warning.
cr_fit <- function(family, d, full) {
  f <- reformulate(full, response = "y")
  withCallingHandlers(
    if (is.null(family)) lm(f, data = d)
    else glm(f, family = family, data = d, control = cr_ctl),
    warning = function(w) stop("replicate fit warned: ", conditionMessage(w)))
}

cr_cases <- list()

# cr_case runs one case: the full model y ~ full, the tested terms the
# ones restricted lacks; resample jackknife (leave-one-out, or by the
# cluster g) or bootstrap (B index sets).
cr_case <- function(name, family, link, d, restricted, full, resample, by = FALSE, B = 200) {
  n <- nrow(d)
  m1 <- cr_fit(family, d, full)
  tested <- setdiff(full, restricted)
  out <- list(name = name, family = if (is.null(family)) "gaussian" else tolower(family$family), link = link,
    resample = resample, jackknife_by = by, restricted = I(restricted), full = I(full), terms = I(tested))
  if (resample == "jackknife") {
    groups <- if (by) split(seq_len(n), d$g) else as.list(seq_len(n))
    reps <- t(sapply(groups, function(gi) coef(cr_fit(family, d[setdiff(seq_len(n), gi), ], full))))
    g <- nrow(reps)
    cen <- sweep(reps, 2, colMeans(reps))
    V <- (g - 1) / g * crossprod(cen)
  } else {
    samples <- lapply(seq_len(B), function(b) sample.int(n, n, replace = TRUE))
    reps <- t(sapply(samples, function(s) coef(cr_fit(family, d[s, ], full))))
    V <- cov(reps)
    out$samples <- lapply(samples, function(s) cr_int(s - 1))
  }
  dimnames(V) <- list(names(coef(m1)), names(coef(m1)))
  lh <- linearHypothesis(m1, paste(tested, "= 0"), vcov. = V, test = "Chisq")
  stat <- lh[2, "Chisq"]
  p <- lh[2, "Pr(>Chisq)"]
  b <- coef(m1)[tested]
  hand <- drop(t(b) %*% solve(V[tested, tested, drop = FALSE], b))
  if (abs(stat - hand) > 1e-12 * abs(hand)) stop(name, ": linearHypothesis ", stat, " vs hand ", hand)
  if (abs(p - pchisq(stat, length(tested), lower.tail = FALSE)) > 1e-14) stop(name, ": p mismatch")
  out$rows <- lapply(seq_len(n), function(i) cr_g17(c(d$x1[i], d$x2[i], d$x3[i], d$y[i], d$g[i])))
  out$coefficients <- cr_g17(coef(m1))
  out$replicate_cov <- lapply(seq_len(nrow(V)), function(i) cr_g17(V[i, ]))
  out$statistic <- cr_num(stat)
  out$df <- length(tested)
  out$p_value <- cr_num(p)
  cr_cases[[length(cr_cases) + 1]] <<- out
}

set.seed(20261125)
cr_design <- function(n) data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(runif(n, -1, 1), 3),
                                     x3 = round(runif(n, 0, 1), 3), g = rep(1:12, length.out = n))

d <- cr_design(60)
d$y <- 1 + 0.8 * d$x1 - 0.5 * d$x2 + 0.3 * d$x3 + rnorm(60, sd = 0.7)
cr_case("ols_jackknife", NULL, "identity", d, c("x1"), c("x1", "x2", "x3"), "jackknife")
cr_case("ols_bootstrap", NULL, "identity", d, c("x1"), c("x1", "x2", "x3"), "bootstrap")

d <- cr_design(120)
d$y <- rbinom(120, 1, plogis(-0.3 + 1.1 * d$x1 - 0.9 * d$x2 + 0.4 * d$x3))
cr_case("logit_jackknife_by", binomial("logit"), "logit", d, c("x1"), c("x1", "x2", "x3"), "jackknife", by = TRUE)
cr_case("logit_bootstrap", binomial("logit"), "logit", d, c("x1", "x3"), c("x1", "x2", "x3"), "bootstrap")

d <- cr_design(100)
d$y <- rpois(100, exp(0.4 + 0.5 * d$x1 - 0.6 * d$x2 + 0.2 * d$x3))
cr_case("poisson_jackknife", poisson("log"), "log", d, c("x1", "x2"), c("x1", "x2", "x3"), "jackknife")

d <- cr_design(80)
d$y <- rgamma(80, shape = 4, rate = 4 / exp(0.5 + 0.4 * d$x1 - 0.3 * d$x2))
cr_case("gamma_log_jackknife", Gamma("log"), "log", d, c("x1"), c("x1", "x2"), "jackknife")

write_golden("compare_resample", list(
  description = paste(
    "Wald nested-model comparison over a resampled fit (U69 #642): manual replicate loops of the",
    "full model in base R; V the jackknife (g-1)/g crossprod or the bootstrap cov() over the recorded",
    "index sets (samples, 0-based); statistic and p from car::linearHypothesis(m1, vcov. = V,",
    "test = 'Chisq') on the tested terms. Rows are [x1, x2, x3, y, g]."),
  packages = list(R = R.version.string, car = as.character(packageVersion("car")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  seed = 20261125,
  cases = cr_cases
))
