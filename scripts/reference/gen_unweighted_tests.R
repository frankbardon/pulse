# gen_unweighted_tests.R — R oracle for the unweighted two-group and
# contingency tests (U64 E2-S1, #616): TEST_T (one-sample and two-sample),
# TEST_WELCH, TEST_Z_TWO_SAMPLE, TEST_CHISQ (independence and goodness of
# fit), TEST_MANN_WHITNEY_U, TEST_KRUSKAL_WALLIS and TEST_PROP_Z (two
# proportions and one proportion). Before it these were pinned only to a
# 4-decimal scipy value or to hand-computed figures; the tight pin was the
# weighted reference.
#
# Sourced at the end of gen_reference.R (`make reference`), reusing its
# out_dir, write_golden() and num(); runnable on its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_unweighted_tests.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the goldens are committed. Base R
# (stats) + jsonlite 2.0.0 only, no other package pin; refuses any R but
# 4.6.1. It draws from its own seeds, so no other golden moves.
#
# Each case carries its inputs verbatim at %.17g, so the Go test
# (internal/processing/test_unweighted_oracle_test.go) replays the very
# doubles R read, through the operator's RowTest (UpdateRow per record,
# then Finalize), at relative 1e-10.
#
# Goldens written, with the definitional choices that make R compute
# Pulse's definition (each stated, none hidden). Each opt-in option of
# #644 (pooled t, exact Mann-Whitney p, Yates' chi-square, prop.test's
# continuity correction and Wilson interval) is pinned beside the
# default it changes (internal/processing/test_options_oracle_test.go):
#   * test_t.json — t.test. One-sample: t.test(x, mu, conf.level =
#     1 - alpha): t, df, p, the interval on the mean. Two-sample:
#     t.test(a, b, var.equal = FALSE): Welch t, the Welch–Satterthwaite
#     df, p, the interval on mean(a) - mean(b). a is the alphabetically
#     first group (Pulse's order without params.reference). TEST_T with
#     split_by and TEST_WELCH are both Welch by default, so every
#     two-sample case runs both; TEST_T params.variance "pooled" is
#     t.test(var.equal = TRUE), stored as pooled_*.
#   * test_z_two_sample.json — no stock R function: z = (mean(a) -
#     mean(b)) / sqrt(var(a)/n_a + var(b)/n_b) on the sample (n - 1)
#     variances, p = 2 * pnorm(-|z|), interval diff -/+ qnorm(1 -
#     alpha/2) * se.
#   * test_chisq.json — chisq.test(table, correct = FALSE): Pulse's
#     default applies no Yates' continuity correction, which chisq.test
#     does by default on a 2 x 2 table; that figure is kept as r_yates_*
#     there and is what params.correct reproduces. The goodness-of-fit form (params.expected) is chisq.test(x,
#     p = expected).
#   * test_mann_whitney_u.json — wilcox.test(a, b, exact = FALSE, correct
#     = TRUE): W = U_a, the tie-corrected normal approximation with the
#     0.5 continuity correction, Pulse's default. wilcox.test's DEFAULT
#     on small samples (n < 50) is the exact distribution (r_p_exact,
#     kept on the untied cases); exact_p_value is wilcox.test(exact =
#     TRUE) — R 4.6 conditions on the tied mid-ranks — which
#     params.exact reproduces wherever Pulse computes it.
#     z and var_u are wilcox.test's own z and SIGMA^2, re-derived here and
#     checked against its p.
#   * test_kruskal_wallis.json — kruskal.test(x, g): tie-corrected H, df,
#     p; per-group rank sums and the tie factor 1 - sum(t^3 - t)/(N^3 - N).
#     H is evaluated in deviation form, 12/(N(N+1)) sum (S_g - n_g (N +
#     1)/2)^2 / n_g, which is exact on mid-ranks; kruskal.test's own
#     form cancels as H -> 0 (1.4e-8 relative on near_null_tie), so its
#     figures ride beside as r_statistic / r_p_value (the U08 refinement
#     pattern), asserted equal to 1e-11 on every other case.
#   * test_prop_z.json — prop.test(x, n, correct = FALSE). Two
#     proportions: z = sign(p_a - p_b) * sqrt(X-squared) (the pooled z),
#     p, and the interval, the unpooled Wald interval prop.test reports
#     (no case lands outside [-1, 1], where prop.test clips it and Pulse
#     does not). One proportion (params.p0): z = sign(p - p0) *
#     sqrt(X-squared), p. Its interval is NOT prop.test's: prop.test
#     reports the Wilson score interval on p, Pulse the Wald interval on
#     p - p0, diff -/+ qnorm(1 - alpha/2) * sqrt(p (1 - p) / n), computed
#     here in that form (prop.test's Wilson bounds kept beside as
#     r_wilson_*, which params.interval "wilson" reproduces on p - p0).
#     yates_* are prop.test(correct = TRUE) (params.correct): its
#     statistic, p and interval (two proportions); one proportion, its
#     Wilson-cc bounds as r_yates_wilson_* and Pulse's corrected Wald
#     interval on p - p0, widened by prop.test's own YATES / n.

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_unweighted_tests.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("jsonlite")) != "2.0.0") stop("gen_unweighted_tests.R pins jsonlite 2.0.0, got ", packageVersion("jsonlite"))

ut_vec <- function(v) lapply(as.numeric(v), num)
ut_meta <- function(operator, r_fn, note) {
  list(
    generator = "scripts/reference/gen_unweighted_tests.R",
    r_version = R.version.string,
    packages = list(
      stats = as.character(packageVersion("stats")),
      jsonlite = as.character(packageVersion("jsonlite"))
    ),
    primitive = operator,
    r_function = r_fn,
    note = note
  )
}
ut_close <- function(what, got, want, tol = 1e-14) {
  if (abs(got - want) > tol * max(abs(want), 1e-300)) stop(what, ": re-derived ", got, " vs R ", want)
}

# ------------------------------------------------------------------ TEST_T
t_one <- function(name, x, mu, alpha = 0.05) {
  tt <- t.test(x, mu = mu, conf.level = 1 - alpha)
  list(
    name = name, variant = "one_sample", values = ut_vec(x), mu = num(mu), alpha = num(alpha),
    statistic = num(unname(tt$statistic)), df = num(unname(tt$parameter)),
    p_value = num(tt$p.value),
    ci_low = num(tt$conf.int[1]), ci_high = num(tt$conf.int[2]),
    n = length(x), mean = num(mean(x)), variance = num(var(x))
  )
}
# offset: the common offset c a large-offset case was built on (0 for
# the ordinary cases). Its inputs are c + d with d on a 2^-6 grid, so
# every a - c is exact and R runs t.test on the deviations a - c, b - c:
# the statistic, df, p, interval, diff and variances are shift-invariant,
# so this is the exact test on the input doubles. t.test(a, b) on the
# levels themselves rounds each mean to the offset's ulp first (2e-4
# relative on diff at 1e12) and is not the reference (#637). The means
# stay the levels, mean(a) and mean(b).
t_two <- function(name, a, b, alpha = 0.05, offset = 0) {
  if (offset != 0 && (any((a - offset) + offset != a) || any((b - offset) + offset != b))) stop(name, ": a - offset is not exact")
  da <- a - offset; db <- b - offset
  tt <- t.test(da, db, var.equal = FALSE, conf.level = 1 - alpha)
  out <- list(
    name = name, variant = "welch_two_sample", a = ut_vec(a), b = ut_vec(b), alpha = num(alpha),
    statistic = num(unname(tt$statistic)), df = num(unname(tt$parameter)),
    p_value = num(tt$p.value),
    ci_low = num(tt$conf.int[1]), ci_high = num(tt$conf.int[2]),
    n = c(length(a), length(b)),
    mean = ut_vec(c(mean(a), mean(b))), variance = ut_vec(c(var(da), var(db))),
    diff = num(mean(da) - mean(db))
  )
  # params.variance "pooled" (#644): t.test(var.equal = TRUE), Student's
  # pooled-variance t on the same deviations.
  pt <- t.test(da, db, var.equal = TRUE, conf.level = 1 - alpha)
  sp2 <- ((length(da) - 1) * var(da) + (length(db) - 1) * var(db)) / (length(da) + length(db) - 2)
  ut_close(paste(name, "pooled t"), (mean(da) - mean(db)) / sqrt(sp2 * (1 / length(da) + 1 / length(db))), unname(pt$statistic), 1e-12)
  out$pooled_statistic <- num(unname(pt$statistic))
  out$pooled_df <- num(unname(pt$parameter))
  out$pooled_p_value <- num(pt$p.value)
  out$pooled_ci_low <- num(pt$conf.int[1])
  out$pooled_ci_high <- num(pt$conf.int[2])
  out$pooled_variance <- num(sp2)
  if (offset != 0) out$offset <- num(offset)
  out
}
set.seed(64201)
t_cases <- list(
  t_one("one_sample_n12", round(rnorm(12, 5.4, 1.2), 2), 5),
  t_one("one_sample_n40_mu0", round(rnorm(40, 0.2, 1), 3), 0),
  t_one("one_sample_alpha10", round(rnorm(25, 101, 4), 1), 100, alpha = 0.10),
  t_one("one_sample_tiny_p", round(rnorm(60, 3, 0.5), 3), 0),
  t_one("one_sample_n2", c(1.5, 2.25), 0),
  t_one("one_sample_offset_1e3", round(rnorm(30, 1000.3, 0.8), 3), 1000)
)
two_sample_inputs <- list(
  list("equal_n", round(rnorm(20, 10, 2), 2), round(rnorm(20, 11, 2), 2), 0.05),
  list("unequal_n_and_variance", round(rnorm(12, 0, 1), 3), round(rnorm(31, 0.6, 3), 3), 0.05),
  list("small_2x3", c(1.2, 2.9), c(3.1, 4.4, 5.0), 0.05),
  list("tiny_p", round(rnorm(80, 0, 1), 3), round(rnorm(70, 2.5, 1.2), 3), 0.05),
  list("alpha_01", round(rnorm(15, 50, 5), 1), round(rnorm(18, 47, 6), 1), 0.01),
  list("offset_1e3", round(rnorm(25, 1000, 1), 3), round(rnorm(28, 1000.4, 1.5), 3), 0.05),
  list("null_like", round(rnorm(30, 7, 1), 2), round(rnorm(33, 7, 1), 2), 0.05)
)
# Large common offsets (#637): c + d, d on a 2^-6 grid so c + d is
# exact. The deviations' spread is O(1) and the offset's ulp at 1e12 is
# 1.2e-4, so a mean held at the data's magnitude carries an error four
# digits below the difference it feeds: what the two-sample tests used
# to lose.
set.seed(64205)
offset_dev <- function(n, mu, sd) round(rnorm(n, mu, sd) * 64) / 64
for (off in list(list("offset_1e6", 1e6), list("offset_1e8", 1e8), list("offset_1e12", 1e12))) {
  two_sample_inputs[[length(two_sample_inputs) + 1]] <- list(off[[1]], off[[2]] + offset_dev(24, 0, 1), off[[2]] + offset_dev(27, 0.45, 1.3), 0.05, off[[2]])
}
two_sample_inputs[[length(two_sample_inputs) + 1]] <- list("offset_1e12_unequal_n", 1e12 + offset_dev(9, 3, 0.6), 1e12 + offset_dev(40, 2.6, 2.2), 0.05, 1e12)
for (s in two_sample_inputs) t_cases[[length(t_cases) + 1]] <- t_two(s[[1]], s[[2]], s[[3]], s[[4]], if (length(s) >= 5) s[[5]] else 0)
write_golden("test_t", c(ut_meta(
  "TEST_T, TEST_WELCH",
  "t.test(x, mu, conf.level = 1 - alpha) and t.test(a, b, var.equal = FALSE, conf.level = 1 - alpha)",
  "Two-sample cases run TEST_T (split_by) and TEST_WELCH, both Welch; a is the alphabetically first group, the interval is on mean(a) - mean(b). The offset_* cases are c + d with d on a 2^-6 grid; R tests the exact deviations a - c, b - c (shift-invariant), not the levels, whose means round to the offset's ulp."
), list(cases = t_cases)))

# ------------------------------------------------------- TEST_Z_TWO_SAMPLE
z_two <- function(name, a, b, alpha, offset = 0) {
  na <- length(a); nb <- length(b)
  da <- a - offset; db <- b - offset
  diff <- mean(da) - mean(db)
  se <- sqrt(var(da) / na + var(db) / nb)
  z <- diff / se
  zc <- qnorm(1 - alpha / 2)
  out <- list(
    name = name, a = ut_vec(a), b = ut_vec(b), alpha = num(alpha),
    statistic = num(z), p_value = num(2 * pnorm(-abs(z))),
    ci_low = num(diff - zc * se), ci_high = num(diff + zc * se),
    n = c(na, nb), mean = ut_vec(c(mean(a), mean(b))), variance = ut_vec(c(var(da), var(db))),
    diff = num(diff)
  )
  if (offset != 0) out$offset <- num(offset)
  out
}
z_cases <- lapply(two_sample_inputs, function(s) z_two(s[[1]], s[[2]], s[[3]], s[[4]], if (length(s) >= 5) s[[5]] else 0))
write_golden("test_z_two_sample", c(ut_meta(
  "TEST_Z_TWO_SAMPLE",
  "(mean(a) - mean(b)) / sqrt(var(a)/n_a + var(b)/n_b); 2 * pnorm(-|z|); diff -/+ qnorm(1 - alpha/2) * se",
  "No stock R function; the sample (n - 1) variances, the same inputs as test_t.json's two-sample cases (the offset_* cases on the exact deviations a - c, b - c)."
), list(cases = z_cases)))

# --------------------------------------------------------------- TEST_CHISQ
chisq_ind <- function(name, tab) {
  dimnames(tab) <- list(paste0("r", seq_len(nrow(tab))), paste0("c", seq_len(ncol(tab))))
  ct <- suppressWarnings(chisq.test(tab, correct = FALSE))
  out <- list(
    name = name, variant = "independence",
    table = lapply(seq_len(nrow(tab)), function(i) as.numeric(tab[i, ])),
    statistic = num(unname(ct$statistic)), df = num(unname(ct$parameter)),
    p_value = num(ct$p.value), expected_min = num(min(ct$expected))
  )
  if (all(dim(tab) == 2)) {
    y <- suppressWarnings(chisq.test(tab))
    out$r_yates_statistic <- num(unname(y$statistic))
    out$r_yates_p_value <- num(y$p.value)
  }
  out
}
chisq_gof <- function(name, counts, p) {
  ct <- suppressWarnings(chisq.test(counts, p = p))
  list(
    name = name, variant = "goodness_of_fit",
    labels = I(paste0("k", seq_along(counts))), counts = as.numeric(counts), expected_proportions = ut_vec(p),
    statistic = num(unname(ct$statistic)), df = num(unname(ct$parameter)),
    p_value = num(ct$p.value), expected_min = num(min(ct$expected))
  )
}
chisq_cases <- list(
  chisq_ind("two_by_two", matrix(c(12, 5, 7, 16), 2, byrow = TRUE)),
  chisq_ind("three_by_four", matrix(c(10, 14, 8, 20, 12, 9, 15, 11, 6, 18, 13, 7), 3, byrow = TRUE)),
  chisq_ind("zero_cell_low_expected", matrix(c(0, 4, 9, 6, 3, 2), 2, byrow = TRUE)),
  chisq_ind("tiny_p", matrix(c(180, 20, 30, 170), 2, byrow = TRUE)),
  chisq_ind("independent_like", matrix(c(25, 26, 24, 50, 49, 51), 2, byrow = TRUE)),
  chisq_ind("five_by_three", matrix(c(5, 9, 12, 7, 7, 8, 11, 6, 3, 4, 10, 15, 9, 9, 9), 5, byrow = TRUE)),
  # |O - E| = 0.24 < 0.5: Yates' term is min(0.5, |O - E|), so the
  # corrected statistic is 0 (params.correct, #644).
  chisq_ind("two_by_two_small_deviation", matrix(c(5, 5, 5, 6), 2, byrow = TRUE)),
  chisq_gof("gof_four_categories", c(30, 22, 28, 20), c(0.25, 0.25, 0.3, 0.2)),
  chisq_gof("gof_skewed", c(5, 40, 15), c(0.1, 0.6, 0.3)),
  chisq_gof("gof_tiny_p", c(150, 10, 40), c(1 / 3, 1 / 3, 1 / 3))
)
write_golden("test_chisq", c(ut_meta(
  "TEST_CHISQ",
  "chisq.test(table, correct = FALSE); chisq.test(counts, p = expected)",
  "Pulse's default applies no Yates' correction; on a 2 x 2 table chisq.test's default (corrected) figure is kept as r_yates_*, which params.correct reproduces."
), list(cases = chisq_cases)))

# ------------------------------------------------------ TEST_MANN_WHITNEY_U
mw_case <- function(name, a, b) {
  wt <- suppressWarnings(wilcox.test(a, b, exact = FALSE, correct = TRUE))
  na <- length(a); nb <- length(b); N <- na + nb
  r <- rank(c(a, b))
  ties <- table(r)
  w <- sum(r[seq_len(na)]) - na * (na + 1) / 2
  var_u <- (na * nb / 12) * ((N + 1) - sum(ties^3 - ties) / (N * (N - 1)))
  d <- w - na * nb / 2
  z <- (d - sign(d) * 0.5) / sqrt(var_u)
  ut_close(paste(name, "W"), w, unname(wt$statistic))
  ut_close(paste(name, "p from z"), 2 * min(pnorm(z), pnorm(z, lower.tail = FALSE)), wt$p.value)
  out <- list(
    name = name, a = ut_vec(a), b = ut_vec(b),
    u_a = num(w), u_b = num(na * nb - w), u_min = num(min(w, na * nb - w)),
    r_a = num(sum(r[seq_len(na)])), mu_u = num(na * nb / 2), var_u = num(var_u), z = num(z),
    p_value = num(wt$p.value), ties = length(ties) < N
  )
  if (length(ties) == N && N < 50) {
    out$r_p_exact <- num(wilcox.test(a, b, exact = TRUE)$p.value)
  }
  # params.exact (#644): R 4.6's exact permutation p, ties included,
  # wherever Pulse computes it (min(n_a, n_b) * N <= 5000); past that
  # Pulse keeps the approximation and warns, so no figure is stored.
  if (min(na, nb) * N <= 5000) {
    out$exact_p_value <- num(wilcox.test(a, b, exact = TRUE)$p.value)
  }
  out
}
set.seed(64203)
mw_cases <- list(
  mw_case("small_untied_5x6", c(1.1, 2.4, 3.3, 5.0, 6.2), c(2.0, 4.1, 4.8, 6.9, 7.5, 8.1)),
  mw_case("heavy_ties", c(1, 1, 2, 2, 2, 3, 3, 4), c(2, 3, 3, 3, 4, 4, 5, 5, 5)),
  mw_case("rounded_30x35", round(rnorm(30, 10, 2)), round(rnorm(35, 11, 2))),
  mw_case("untied_60x55", round(rnorm(60, 0, 1), 4), round(rnorm(55, 0.4, 1), 4)),
  mw_case("separated_tiny_p", round(rnorm(70, 0, 1), 3), round(rnorm(75, 3, 1), 3)),
  mw_case("centred_u_equals_mu", c(1, 4), c(2, 3)),
  mw_case("n2_each", c(0.5, 3.5), c(1.5, 2.5, 9)),
  # U_a above mu_U (a tends larger), so the continuity correction
  # subtracts; every case above has U_a at or below it.
  mw_case("a_larger_ties", round(rnorm(25, 12, 2)), round(rnorm(20, 10, 2))),
  mw_case("a_larger_untied_6x4", c(5.5, 7.1, 8.4, 9.9, 10.2, 11.7), c(4.1, 6.3, 7.7, 8.0))
)
write_golden("test_mann_whitney_u", c(ut_meta(
  "TEST_MANN_WHITNEY_U",
  "wilcox.test(a, b, exact = FALSE, correct = TRUE)",
  "The normal approximation with tie and continuity correction, Pulse's default; r_p_exact is wilcox.test's default exact p on small untied samples, exact_p_value wilcox.test(exact = TRUE) (ties included) where Pulse's params.exact computes it."
), list(cases = mw_cases)))

# ------------------------------------------------------ TEST_KRUSKAL_WALLIS
kw_case <- function(name, groups, near_null = FALSE) {
  x <- unlist(groups)
  g <- factor(rep(names(groups), lengths(groups)), levels = names(groups))
  kt <- kruskal.test(x, g)
  r <- rank(x)
  ties <- table(x)
  N <- length(x)
  S <- tapply(r, g, sum)
  n <- tapply(r, g, length)
  tie_factor <- 1 - sum(ties^3 - ties) / (N^3 - N)
  # H in deviation form: S - n (N + 1) / 2 is exact on integer and
  # half-integer ranks, so nothing cancels. kruskal.test's 12 sum(S^2 /
  # n) / (N (N + 1)) - 3 (N + 1) cancels as H -> 0 (1.4e-8 relative on
  # near_null_tie below); its figures ride beside as r_statistic /
  # r_p_value, and must agree to 1e-11 on every other case.
  h <- 12 / (N * (N + 1)) * sum((S - n * (N + 1) / 2)^2 / n) / tie_factor
  df <- length(groups) - 1
  if (!near_null) ut_close(paste(name, "H"), h, unname(kt$statistic), 1e-11)
  list(
    name = name, groups = I(names(groups)), values = lapply(groups, ut_vec),
    statistic = num(h), df = num(df), p_value = num(pchisq(h, df, lower.tail = FALSE)),
    rank_sums = ut_vec(S), tie_factor = num(tie_factor),
    r_statistic = num(unname(kt$statistic)), r_p_value = num(kt$p.value)
  )
}
# near_null_tie: 300 ranks dealt a, b, c, c, b, a in blocks of six, so
# the three rank sums are equal (H = 0), then one tie between a's rank 1
# and b's rank 2 moves the sums by -/+ 0.5: H = 6.6e-7.
kw_snake <- function() {
  g <- rep(c("a", "b", "c", "c", "b", "a"), 50)
  x <- as.numeric(seq_along(g))
  x[2] <- 1
  split(x, g)
}
set.seed(64204)
kw_cases <- list(
  kw_case("three_untied", list(a = c(2.1, 3.4, 1.9, 5.5), b = c(4.2, 6.1, 5.9, 7.3, 6.6), c = c(1.2, 0.8, 2.6))),
  kw_case("heavy_ties", list(a = c(1, 2, 2, 3, 3, 3), b = c(2, 3, 4, 4, 5), c = c(3, 4, 5, 5, 5, 6))),
  kw_case("five_groups", list(a = round(rnorm(12, 0), 2), b = round(rnorm(10, 0.3), 2), c = round(rnorm(14, 0.6), 2),
                              d = round(rnorm(9, 0.2), 2), e = round(rnorm(11, 1), 2))),
  kw_case("tiny_p", list(a = round(rnorm(40, 0), 3), b = round(rnorm(40, 2), 3), c = round(rnorm(40, 4), 3))),
  kw_case("two_groups", list(a = round(rnorm(15, 5, 2)), b = round(rnorm(18, 6, 2)))),
  kw_case("null_like", list(a = round(rnorm(30, 0), 2), b = round(rnorm(30, 0), 2), c = round(rnorm(30, 0), 2))),
  kw_case("near_null_tie", kw_snake(), near_null = TRUE)
)
write_golden("test_kruskal_wallis", c(ut_meta(
  "TEST_KRUSKAL_WALLIS",
  "kruskal.test(x, g)",
  "Tie-corrected H in deviation form (no cancellation) with its chi-square p; kruskal.test's own figures kept as r_statistic / r_p_value (equal to 1e-11 except near_null_tie). rank_sums from rank() of the pooled sample, groups in alphabetical order."
), list(cases = kw_cases)))

# ------------------------------------------------------------- TEST_PROP_Z
pz_two <- function(name, sa, na, sb, nb, alpha = 0.05) {
  pt <- prop.test(c(sa, sb), c(na, nb), correct = FALSE, conf.level = 1 - alpha)
  pa <- sa / na; pb <- sb / nb
  half <- qnorm(1 - alpha / 2) * sqrt(pa * (1 - pa) / na + pb * (1 - pb) / nb)
  if (pa - pb - half < -1 || pa - pb + half > 1) stop(name, ": prop.test would clip the interval")
  ut_close(paste(name, "ci_low"), pa - pb - half, pt$conf.int[1], 1e-13)
  # params.correct (#644): prop.test(correct = TRUE) — Yates on the
  # statistic, the Wald interval widened by YATES * sum(1/n).
  yt <- prop.test(c(sa, sb), c(na, nb), correct = TRUE, conf.level = 1 - alpha)
  if (yt$conf.int[1] <= -1 || yt$conf.int[2] >= 1) stop(name, ": prop.test would clip the corrected interval")
  list(
    name = name, variant = "two_proportion_pooled", successes = c(sa, sb), n = c(na, nb), alpha = num(alpha),
    statistic = num(sign(pa - pb) * sqrt(unname(pt$statistic))), p_value = num(pt$p.value),
    ci_low = num(pt$conf.int[1]), ci_high = num(pt$conf.int[2]),
    proportion = ut_vec(c(pa, pb)), pooled = num((sa + sb) / (na + nb)),
    yates_statistic = num(sign(pa - pb) * sqrt(unname(yt$statistic))), yates_p_value = num(yt$p.value),
    yates_ci_low = num(yt$conf.int[1]), yates_ci_high = num(yt$conf.int[2])
  )
}
pz_one <- function(name, s, n, p0, alpha = 0.05) {
  pt <- prop.test(s, n, p = p0, correct = FALSE, conf.level = 1 - alpha)
  ph <- s / n
  half <- qnorm(1 - alpha / 2) * sqrt(ph * (1 - ph) / n)
  # params.correct (#644): prop.test(correct = TRUE)'s statistic and its
  # continuity-corrected Wilson interval; Pulse's corrected Wald
  # interval on p - p0 widens by prop.test's own term, YATES / n with
  # YATES = min(0.5, |s - n p0|), computed here in that form.
  yt <- prop.test(s, n, p = p0, correct = TRUE, conf.level = 1 - alpha)
  y <- min(0.5, abs(s - n * p0))
  list(
    name = name, variant = "one_proportion", successes = s, n = n, p0 = num(p0), alpha = num(alpha),
    statistic = num(sign(ph - p0) * sqrt(unname(pt$statistic))), p_value = num(pt$p.value),
    ci_low = num(ph - p0 - half), ci_high = num(ph - p0 + half),
    proportion = num(ph),
    r_wilson_low = num(pt$conf.int[1]), r_wilson_high = num(pt$conf.int[2]),
    yates_statistic = num(sign(ph - p0) * sqrt(unname(yt$statistic))), yates_p_value = num(yt$p.value),
    yates_ci_low = num(ph - p0 - half - y / n), yates_ci_high = num(ph - p0 + half + y / n),
    r_yates_wilson_low = num(yt$conf.int[1]), r_yates_wilson_high = num(yt$conf.int[2])
  )
}
pz_cases <- list(
  pz_two("two_balanced", 45, 100, 60, 100),
  pz_two("two_unbalanced", 18, 40, 70, 210),
  pz_two("two_small", 3, 9, 6, 11),
  pz_two("two_tiny_p", 400, 1000, 600, 1000),
  pz_two("two_alpha_10", 33, 80, 41, 85, alpha = 0.10),
  pz_two("two_equal", 20, 50, 30, 75),
  pz_one("one_p0_half", 58, 100, 0.5),
  pz_one("one_p0_low", 9, 60, 0.05),
  pz_one("one_alpha_01", 140, 400, 0.4, alpha = 0.01),
  pz_one("one_tiny_p", 900, 1000, 0.7),
  # every trial a success: the Wilson upper bound clips to 1 (#644)
  pz_one("one_all_success", 20, 20, 0.8)
)
write_golden("test_prop_z", c(ut_meta(
  "TEST_PROP_Z",
  "prop.test(x, n, correct = FALSE): z = sign(diff) * sqrt(X-squared)",
  "Two proportions: prop.test's p and Wald interval. One proportion: prop.test's p; the interval is Pulse's Wald interval on p - p0, prop.test's Wilson bounds kept as r_wilson_*. yates_* / r_yates_wilson_* are prop.test(correct = TRUE)."
), list(cases = pz_cases)))
