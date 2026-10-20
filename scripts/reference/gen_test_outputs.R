# gen_test_outputs.R — R oracle for assembled TEST_* outputs (U36 E1-S2).
#
# The primitives goldens (gen_reference.R) pin the distribution
# functions; this script pins whole operator outputs — statistic, df,
# p-value and the per-family extras — on unweighted inputs that each
# case carries verbatim, so the Go test replays the very doubles R read.
# Sourced at the end of gen_reference.R (`make reference`), reusing its
# out_dir, write_golden(), num() and the studentized-range oracle
# tukey_surv(); it cannot run on its own.
#
# Goldens written:
#   * test_anova_rm.json — TEST_ANOVA_RM: summary(aov(y ~ cond +
#     Error(subj / cond))) on the complete subjects: F, df, p, the sums
#     of squares of both strata, condition means, partial eta squared.
#     Incomplete subjects are dropped before the fit, as Pulse does.
#   * test_tukey_hsd.json — TEST_TUKEY_HSD: TukeyHSD(aov(y ~ g)). Pulse
#     reads the group means, counts, MS_within and df_within, which the
#     case records from the same fit. Per pair (a < b alphabetically)
#     diff = mean_a - mean_b (the negated R diff), se, q = |diff| / se.
#     p_adj and the interval follow the U08 refinement pattern: stock
#     ptukey() is accurate to ~1e-8 and qtukey() to ~1e-4, so p_adj is
#     gen_reference.R's nested-quadrature survival tukey_surv(q, k, df)
#     and q_critical a uniroot() on it; R's own TukeyHSD p adj, lwr and
#     upr are kept beside them (r_p_adj, r_lwr, r_upr).
#   * test_trend.json — TEST_TREND (Mann-Kendall over the series order).
#     Kendall::MannKendall computes in SINGLE precision (its Fortran
#     tauk2 takes as.single inputs), so it cannot be a 1e-10 oracle; it is
#     recorded beside (r_kendall_*) and the Go test holds Pulse to it at
#     single precision. The double-precision oracle is base R:
#     cor.test(seq_along(y), y, method = "kendall", exact = FALSE,
#     continuity = TRUE) for z and p (S with the continuity correction
#     over the tie-adjusted variance — Mann-Kendall's own form),
#     cor(seq_along(y), y, method = "kendall") for tau (tau-b, which is
#     Kendall::MannKendall's S / D), S as the pair-sign sum and var_s
#     as the tie-adjusted n(n-1)(2n+5)/18 formula.
#   * test_ks.json — TEST_KS: D = ks.test(a, b)$statistic. Pulse's p is
#     Stephens' small-sample correction of the limiting distribution
#     (Stephens 1970; Numerical Recipes 14.3), not ks.test's exact or
#     raw-asymptotic p, so the oracle p is base R's limiting Kolmogorov
#     survival C_pkolmogorov_two_limit at lambda = (en + 0.12 + 0.11 /
#     en) * D, en = sqrt(n1 n2 / (n1 + n2)). ks.test's exact and
#     asymptotic p are kept beside it (r_p_exact, r_p_asymptotic).
#   * test_kendall_tau.json — TEST_KENDALL_TAU: cor.test(x, y, method =
#     "kendall") at its defaults (exact when untied and n < 50, else the
#     uncorrected normal approximation); tau-b, z and p. Most cases tie.
#   * test_pearson_r.json — TEST_PEARSON_R: cor.test(x, y, conf.level =
#     1 - alpha): r, t, df, p and the Fisher-z interval.
#
# Data are seeded and rounded to a few decimals (ties where wanted);
# every input is written at %.17g so nothing depends on the RNG in Go.

stopifnot(exists("out_dir"), exists("write_golden"), exists("num"), exists("tukey_surv"))

to_pinned <- c(Kendall = "2.2.2")
for (p in names(to_pinned)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != to_pinned[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, to_pinned[[p]]))
  }
}
if (R.version.string != "R version 4.6.1 (2026-06-24)") stop("the oracle is pinned to R 4.6.1, got ", R.version.string)
suppressPackageStartupMessages(library(Kendall))

to_meta <- function(operator, r_fn, note, extra = list()) {
  list(
    generator = "scripts/reference/gen_test_outputs.R",
    r_version = R.version.string,
    packages = c(list(
      stats = as.character(packageVersion("stats")),
      jsonlite = as.character(packageVersion("jsonlite"))
    ), extra),
    primitive = operator,
    r_function = r_fn,
    note = note
  )
}
to_vec <- function(v) lapply(as.numeric(v), num)

# ------------------------------------------------------------ TEST_ANOVA_RM
rm_case <- function(name, subj, cond, y) {
  d <- data.frame(subj = subj, cond = cond, y = y, stringsAsFactors = FALSE)
  conds <- sort(unique(d$cond))
  complete <- names(which(tapply(d$cond, d$subj, function(cc) all(conds %in% cc))))
  dc <- d[d$subj %in% complete, ]
  dc$subj <- factor(dc$subj)
  dc$cond <- factor(dc$cond, levels = conds)
  fit <- summary(aov(y ~ cond + Error(subj / cond), data = dc))
  within <- fit[["Error: subj:cond"]][[1]]
  between <- fit[["Error: subj"]][[1]]
  ss_cond <- within["cond", "Sum Sq"]
  ss_err <- within["Residuals", "Sum Sq"]
  list(
    name = name,
    subject = d$subj, condition = d$cond, value = to_vec(d$y),
    conditions = conds,
    complete_subjects = length(complete),
    dropped_subjects = length(unique(d$subj)) - length(complete),
    statistic = num(within["cond", "F value"]),
    df_treatment = num(within["cond", "Df"]),
    df_error = num(within["Residuals", "Df"]),
    p_value = num(within["cond", "Pr(>F)"]),
    ss_treatment = num(ss_cond),
    ss_error = num(ss_err),
    ss_between_subjects = num(between["Residuals", "Sum Sq"]),
    grand_mean = num(mean(dc$y)),
    condition_means = to_vec(tapply(dc$y, dc$cond, mean)[conds]),
    partial_eta_squared = num(ss_cond / (ss_cond + ss_err))
  )
}
set.seed(36201)
rm_cases <- list()
local({
  s <- sprintf("s%02d", 1:6)
  base <- round(rnorm(6, 50, 6), 2)
  eff <- c(base = 0, drug_a = 1.5, drug_b = 3.2)
  g <- expand.grid(subj = s, cond = names(eff), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + eff[g$cond] + round(rnorm(nrow(g), 0, 1.4), 2)
  rm_cases[[1]] <<- rm_case("six_subjects_three_conditions", g$subj, g$cond, y)

  s <- sprintf("p%02d", 1:9)
  base <- round(rnorm(9, 20, 3), 1)
  eff <- c(t1 = 0, t2 = 0.4, t3 = -0.3, t4 = 0.9)
  g <- expand.grid(subj = s, cond = names(eff), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + eff[g$cond] + round(rnorm(nrow(g), 0, 0.8), 1)
  # p09 misses t3: Pulse drops the subject; the fit uses the other 8.
  drop <- g$subj == "p09" & g$cond == "t3"
  rm_cases[[2]] <<- rm_case("nine_subjects_one_incomplete", g$subj[!drop], g$cond[!drop], y[!drop])

  s <- sprintf("u%d", 1:5)
  base <- round(runif(5, 100, 140))
  g <- expand.grid(subj = s, cond = c("after", "before"), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + ifelse(g$cond == "after", -4, 0) + round(rnorm(nrow(g), 0, 3))
  rm_cases[[3]] <<- rm_case("two_conditions_integer_scores", g$subj, g$cond, y)
})
write_golden("test_anova_rm", c(to_meta(
  "TEST_ANOVA_RM",
  "summary(aov(y ~ cond + Error(subj / cond))) on the complete subjects",
  "statistic / df / p from the subj:cond stratum; ss_between_subjects is the subj stratum's residual SS."
), list(cases = rm_cases)))

# ----------------------------------------------------------- TEST_TUKEY_HSD
tk_case <- function(name, g, y, alpha = 0.05) {
  g <- factor(g, levels = sort(unique(g)))
  fit <- aov(y ~ g)
  df_w <- fit$df.residual
  ms_w <- sum(fit$residuals^2) / df_w
  means <- tapply(y, g, mean)
  ns <- tapply(y, g, length)
  k <- nlevels(g)
  hsd <- TukeyHSD(fit, conf.level = 1 - alpha)$g
  r_q <- qtukey(alpha, k, df_w, lower.tail = FALSE)
  fn <- function(q) log(tukey_surv(q, k, df_w)) - log(alpha)
  q_crit <- uniroot(fn, c(r_q * 0.9, r_q * 1.1), extendInt = "downX",
                    tol = 1e-14 * r_q, maxiter = 200)$root
  levs <- levels(g)
  pairs <- list()
  for (i in 1:(k - 1)) for (j in (i + 1):k) {
    a <- levs[i]; b <- levs[j]
    row <- hsd[paste0(b, "-", a), ]
    diff <- means[[a]] - means[[b]]
    se <- sqrt(ms_w * (1 / ns[[a]] + 1 / ns[[b]]) / 2)
    q <- abs(diff) / se
    pairs[[length(pairs) + 1]] <- list(
      a = a, b = b,
      diff = num(diff), se = num(se), q = num(q),
      p_adj = num(tukey_surv(q, k, df_w)),
      ci_low = num(diff - q_crit * se), ci_high = num(diff + q_crit * se),
      r_diff = num(row[["diff"]]), r_lwr = num(row[["lwr"]]), r_upr = num(row[["upr"]]),
      r_p_adj = num(row[["p adj"]])
    )
  }
  list(
    name = name, alpha = num(alpha),
    groups = levs, means = to_vec(means), n = to_vec(ns),
    ms_within = num(ms_w), df_within = num(df_w),
    q_critical = num(q_crit), r_qtukey = num(r_q),
    comparisons = pairs
  )
}
set.seed(36202)
tk_cases <- list()
local({
  g <- rep(c("a", "b", "c"), times = c(8, 8, 8))
  y <- round(rnorm(24, rep(c(10, 11.2, 12.9), each = 8), 1.6), 2)
  tk_cases[[1]] <<- tk_case("three_groups_balanced", g, y)

  g <- rep(c("east", "north", "south", "west"), times = c(6, 11, 9, 7))
  y <- round(rnorm(length(g), c(east = 5, north = 5.4, south = 6.8, west = 5.1)[g], 1.1), 2)
  tk_cases[[2]] <<- tk_case("four_groups_unbalanced", g, y)

  g <- rep(c("g1", "g2", "g3", "g4", "g5"), times = c(4, 5, 4, 6, 5))
  y <- round(rnorm(length(g), c(g1 = 3, g2 = 3.1, g3 = 3.3, g4 = 4.2, g5 = 2.6)[g], 0.7), 2)
  tk_cases[[3]] <<- tk_case("five_groups_alpha_01", g, y, alpha = 0.01)
})
write_golden("test_tukey_hsd", c(to_meta(
  "TEST_TUKEY_HSD",
  "TukeyHSD(aov(y ~ g)); p_adj / q_critical refined on tukey_surv() (gen_reference.R)",
  "diff = mean_a - mean_b for a < b (the negated R diff); r_p_adj / r_lwr / r_upr are stock TukeyHSD (ptukey ~1e-8, qtukey ~1e-4)."
), list(cases = tk_cases)))

# ---------------------------------------------------------------- TEST_TREND
tr_case <- function(name, y) {
  n <- length(y)
  idx <- seq_along(y)
  ct <- cor.test(idx, y, method = "kendall", exact = FALSE, continuity = TRUE)
  s <- 0
  for (i in 1:(n - 1)) s <- s + sum(sign(y[(i + 1):n] - y[i]))
  tt <- table(y)
  tt <- tt[tt > 1]
  var_s <- (n * (n - 1) * (2 * n + 5) - sum(tt * (tt - 1) * (2 * tt + 5))) / 18
  mk <- MannKendall(y)
  list(
    name = name, value = to_vec(y),
    statistic = num(unname(ct$statistic)), p_value = num(ct$p.value),
    tau = num(cor(idx, y, method = "kendall")),
    s = num(s), var_s = num(var_s), n = n,
    r_kendall_tau = num(as.numeric(mk$tau)), r_kendall_sl = num(as.numeric(mk$sl)),
    r_kendall_s = num(as.numeric(mk$S)), r_kendall_var_s = num(as.numeric(mk$varS)),
    r_kendall_d = num(as.numeric(mk$D))
  )
}
set.seed(36203)
tr_cases <- list(
  tr_case("rising_with_ties_n15", c(3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9)),
  tr_case("rising_untied_n10", c(1.2, 2.3, 2.1, 3.5, 4.1, 3.9, 5.2, 6.0, 5.8, 7.1)),
  tr_case("noisy_rounded_n30", round(0.04 * (1:30) + rnorm(30, 0, 0.6), 1)),
  tr_case("falling_heavy_ties_n14", c(9, 9, 8, 9, 7, 7, 7, 6, 8, 5, 5, 6, 4, 5)),
  tr_case("no_trend_n12", c(4, 6, 5, 3, 7, 5, 4, 6, 5, 3, 6, 4))
)
write_golden("test_trend", c(to_meta(
  "TEST_TREND",
  "cor.test(seq_along(y), y, method = \"kendall\", exact = FALSE, continuity = TRUE); cor(..., method = \"kendall\"); Kendall::MannKendall as a single-precision cross-check",
  "tau is tau-b (= Kendall::MannKendall's S / D); r_kendall_* are single precision.",
  list(Kendall = as.character(packageVersion("Kendall")))
), list(cases = tr_cases)))

# ------------------------------------------------------------------ TEST_KS
kolm_limit <- function(l) .Call(stats:::C_pkolmogorov_two_limit, l, FALSE, 1e-300)
ks_case <- function(name, a, b) {
  d <- unname(suppressWarnings(ks.test(a, b))$statistic)
  n1 <- length(a); n2 <- length(b)
  en <- sqrt(n1 * n2 / (n1 + n2))
  list(
    name = name, a = to_vec(a), b = to_vec(b),
    statistic = num(d),
    p_value = num(kolm_limit((en + 0.12 + 0.11 / en) * d)),
    r_p_exact = num(suppressWarnings(ks.test(a, b, exact = TRUE))$p.value),
    r_p_asymptotic = num(suppressWarnings(ks.test(a, b, exact = FALSE))$p.value)
  )
}
set.seed(36204)
ks_cases <- list(
  ks_case("shifted_normals_untied", round(rnorm(25, 0, 1), 4), round(rnorm(30, 0.7, 1), 4)),
  ks_case("rounded_with_ties", round(rnorm(40, 10, 2)), round(rnorm(35, 11, 2))),
  ks_case("scale_difference", round(rnorm(50, 0, 1), 3), round(rnorm(45, 0, 2.2), 3)),
  ks_case("small_unequal", c(1, 2, 2, 3, 5), c(2, 2, 4, 4, 6, 7)),
  ks_case("similar_samples", round(runif(60), 3), round(runif(55), 3))
)
write_golden("test_ks", c(to_meta(
  "TEST_KS",
  "ks.test(a, b)$statistic; C_pkolmogorov_two_limit at Stephens' corrected lambda",
  "p_value is the Stephens-corrected asymptotic Pulse documents; r_p_exact / r_p_asymptotic are ks.test's own."
), list(cases = ks_cases)))

# --------------------------------------------------------- TEST_KENDALL_TAU
kt_case <- function(name, x, y) {
  ct <- suppressWarnings(cor.test(x, y, method = "kendall"))
  out <- list(
    name = name, x = to_vec(x), y = to_vec(y),
    ties = any(duplicated(x)) || any(duplicated(y)),
    statistic = num(unname(ct$estimate)), p_value = num(ct$p.value)
  )
  if (names(ct$statistic) == "z") out$z <- num(unname(ct$statistic))
  out
}
set.seed(36205)
kt_cases <- list()
local({
  x <- round(rnorm(30, 0, 1), 1); y <- round(0.5 * x + rnorm(30, 0, 1), 1)
  kt_cases[[1]] <<- kt_case("ties_both_columns_n30", x, y)
  x <- sample(1:5, 40, replace = TRUE); y <- pmin(5, pmax(1, x + sample(-2:2, 40, replace = TRUE)))
  kt_cases[[2]] <<- kt_case("likert_heavy_ties_n40", x, y)
  x <- round(runif(25, 0, 100)); y <- sample(c(0, 1, 2), 25, replace = TRUE)
  kt_cases[[3]] <<- kt_case("ties_in_y_only_n25", x, y)
  x <- round(rnorm(80), 3); y <- round(-0.3 * x + rnorm(80), 3)
  kt_cases[[4]] <<- kt_case("untied_large_n80", x, y)
  x <- c(2.1, 3.4, 1.9, 5.6, 4.4, 6.2, 3.8, 7.1, 5.0, 8.3, 6.6, 9.0)
  y <- c(1.0, 2.2, 1.5, 3.9, 3.1, 4.8, 2.4, 5.5, 4.1, 6.0, 5.2, 7.7)
  kt_cases[[5]] <<- kt_case("untied_exact_n12", x, y)
})
write_golden("test_kendall_tau", c(to_meta(
  "TEST_KENDALL_TAU",
  "cor.test(x, y, method = \"kendall\") at its defaults",
  "statistic is tau-b; z is present only on the normal-approximation path (ties or n >= 50)."
), list(cases = kt_cases)))

# ----------------------------------------------------------- TEST_PEARSON_R
pr_case <- function(name, x, y, alpha = 0.05) {
  ct <- cor.test(x, y, conf.level = 1 - alpha)
  list(
    name = name, alpha = num(alpha), x = to_vec(x), y = to_vec(y),
    statistic = num(unname(ct$estimate)), t = num(unname(ct$statistic)),
    df = num(unname(ct$parameter)), p_value = num(ct$p.value),
    ci_low = num(ct$conf.int[1]), ci_high = num(ct$conf.int[2])
  )
}
set.seed(36206)
pr_cases <- list()
local({
  x <- round(rnorm(40, 50, 10), 2); y <- round(0.6 * x + rnorm(40, 0, 8), 2)
  pr_cases[[1]] <<- pr_case("positive_n40", x, y)
  x <- round(rnorm(25), 3); y <- round(-0.8 * x + rnorm(25, 0, 0.5), 3)
  pr_cases[[2]] <<- pr_case("negative_n25_alpha_01", x, y, alpha = 0.01)
  x <- round(runif(15, 0, 10), 2); y <- round(runif(15, 0, 10), 2)
  pr_cases[[3]] <<- pr_case("weak_n15_alpha_10", x, y, alpha = 0.10)
  pr_cases[[4]] <<- pr_case("smallest_interval_n4", c(1.5, 2.0, 3.7, 4.1), c(2.2, 1.9, 4.0, 5.3))
  x <- round(rnorm(200, 0, 1), 4); y <- round(0.95 * x + rnorm(200, 0, 0.3), 4)
  pr_cases[[5]] <<- pr_case("strong_n200", x, y)
})
write_golden("test_pearson_r", c(to_meta(
  "TEST_PEARSON_R",
  "cor.test(x, y, conf.level = 1 - alpha)",
  "ci_low / ci_high are cor.test's Fisher-z interval."
), list(cases = pr_cases)))
