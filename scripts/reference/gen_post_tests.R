# gen_post_tests.R — R oracle for the post-test (tier-2) twins over rows
# holding nulls (U36 E1-S5).
#
# A post-test reads the materialized result rows, where a cell can be
# null (a WIN_LAG column's leading rows, a nullable field passed
# through). Pulse drops a row with a null in ANY column the test reads,
# listwise — R's default na.action (complete.cases on the used columns)
# — and reports the n it actually used. One case per post family, each
# carrying its rows verbatim (null where R reads NA), the Test spec the
# Go test builds, and the R figures as `expect`: a map from a result path
# ("statistic", "p_value", "df", "details.<key>[.<index>.<key>]") to the
# value. Sourced at the end of gen_reference.R after gen_test_outputs.R
# (`make reference`), reusing out_dir, write_golden(), num(), to_meta(),
# tk_case(), bf_case() and kolm_limit(); it cannot run on its own.
#
# Golden written: test_post_nulls.json. Per family:
#   * TEST_PEARSON_R — cor.test(x, y) on a lag-1 autocorrelation: x is
#     y lagged one row, so its first row is NA (WIN_LAG's natural null),
#     plus one NA in y; r, t, df, p, the Fisher-z interval, n.
#   * TEST_SPEARMAN_R — cor.test(x, y, method = "spearman") at its
#     default (exact AS 89 p, untied); rho, p, n.
#   * TEST_KENDALL_TAU — cor.test(x, y, method = "kendall") at its
#     default (ties: the normal approximation); tau-b, z, p, n.
#   * TEST_PAIRED_T — t.test(x, y, paired = TRUE); t, df, p, mean
#     difference, interval, Cohen's d = mean(d) / sd(d), n.
#   * TEST_WILCOXON_SR — wilcox.test(x, y, paired = TRUE, exact = FALSE,
#     correct = TRUE) (Pulse's asymptotic form); V = w_plus, p; the
#     statistic is min(V, n(n+1)/2 - V); one zero difference dropped.
#   * TEST_ANOVA_F — summary(aov(y ~ g)) on the raw rows of the groups
#     whose summary row (group, mean, n, variance) has no null; F, df,
#     p, SS, eta squared. The raw rows ride along (raw_value, raw_group)
#     for the post == row equivalence test.
#   * TEST_ANOVA_WELCH — oneway.test(y ~ g, var.equal = FALSE), same
#     shape; F, df1, df2, p.
#   * TEST_TUKEY_HSD — tk_case() (gen_test_outputs.R: TukeyHSD, p_adj /
#     q_critical refined on tukey_surv()) on the non-null groups; the
#     MS_within / df_within params are that fit's.
#   * TEST_BROWN_FORSYTHE — bf_case() on the complete rows (NA values
#     and one NA group label dropped first: tapply(median) would read NA).
#   * TEST_SHAPIRO_WILK — nortest::sf.test(x), which drops NA itself.
#   * TEST_KS — ks.test(a, b) (drops NA itself) for D; the Stephens-
#     corrected limiting p as in test_ks.json.
#   * TEST_TREND — cor.test(period, y, method = "kendall", exact = FALSE,
#     continuity = TRUE) with NA in y and one NA period (complete.cases
#     drops both), cor(..., use = "complete.obs") for tau-b, S and the
#     tie-adjusted Var(S) on the complete series.

stopifnot(exists("to_meta"), exists("tk_case"), exists("bf_case"), exists("kolm_limit"))

pn_null <- structure("null", class = "json")
pn_cell <- function(v) {
  if (is.character(v)) return(if (is.na(v)) pn_null else v)
  if (is.na(v)) pn_null else num(v)
}
# pn_rows turns named equal-length columns into a list of row objects.
pn_rows <- function(...) {
  cols <- list(...)
  n <- length(cols[[1]])
  lapply(seq_len(n), function(i) lapply(cols, function(col) pn_cell(col[[i]])))
}
pn_expect <- function(...) lapply(list(...), function(v) num(as.numeric(v)))
pn_case <- function(family, name, spec, rows, n_used, expect, extra = list()) {
  c(list(family = family, name = name, spec = spec, rows = rows, n_used = n_used, expect = expect), extra)
}

set.seed(36501)
pn_cases <- list()

# --------------------------------------------------------- TEST_PEARSON_R
local({
  y <- round(cumsum(rnorm(24, 0.2, 1)), 2)
  x <- c(NA, y[-length(y)])      # WIN_LAG offset 1: the first row is null
  y[9] <- NA                      # and one null in the series itself
  ct <- cor.test(x, y)
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_PEARSON_R", "lag1_autocorrelation_with_nulls",
    list(field = "x", field2 = "y"), pn_rows(x = x, y = y), sum(complete.cases(x, y)),
    pn_expect(statistic = ct$estimate, p_value = ct$p.value, df = ct$parameter,
              details.t = ct$statistic, details.ci_low = ct$conf.int[1], details.ci_high = ct$conf.int[2],
              details.n = sum(complete.cases(x, y))))
})

# -------------------------------------------------------- TEST_SPEARMAN_R
local({
  x <- round(runif(16, 0, 100), 1)
  y <- round(0.4 * x + rnorm(16, 0, 20), 1)
  stopifnot(!any(duplicated(x)), !any(duplicated(y)))
  x[c(2, 11)] <- NA
  y[6] <- NA
  ct <- cor.test(x, y, method = "spearman")
  n <- sum(complete.cases(x, y))
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_SPEARMAN_R", "untied_exact_with_nulls",
    list(field = "x", field2 = "y"), pn_rows(x = x, y = y), n,
    pn_expect(statistic = ct$estimate, p_value = ct$p.value, details.n = n))
})

# ------------------------------------------------------- TEST_KENDALL_TAU
local({
  x <- sample(1:6, 30, replace = TRUE)
  y <- pmin(6, pmax(1, x + sample(-2:2, 30, replace = TRUE)))
  x[c(1, 17)] <- NA
  y[c(4, 17, 25)] <- NA
  ct <- suppressWarnings(cor.test(x, y, method = "kendall"))
  stopifnot(names(ct$statistic) == "z")
  n <- sum(complete.cases(x, y))
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_KENDALL_TAU", "tied_likert_with_nulls",
    list(field = "x", field2 = "y"), pn_rows(x = x, y = y), n,
    pn_expect(statistic = ct$estimate, p_value = ct$p.value, details.z = ct$statistic, details.n = n))
})

# ---------------------------------------------------------- TEST_PAIRED_T
local({
  x <- round(rnorm(18, 50, 8), 2)
  y <- round(x - 2 + rnorm(18, 0, 3), 2)
  x[3] <- NA
  y[c(10, 14)] <- NA
  tt <- t.test(x, y, paired = TRUE)
  ok <- complete.cases(x, y)
  d <- x[ok] - y[ok]
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_PAIRED_T", "before_after_with_nulls",
    list(field = "x", field2 = "y"), pn_rows(x = x, y = y), sum(ok),
    pn_expect(statistic = tt$statistic, p_value = tt$p.value, df = tt$parameter,
              details.mean_diff = tt$estimate, details.ci_low = tt$conf.int[1], details.ci_high = tt$conf.int[2],
              details.effect_size.cohens_d = mean(d) / sd(d), details.n = sum(ok)))
})

# -------------------------------------------------------- TEST_WILCOXON_SR
local({
  x <- round(rnorm(20, 10, 2), 1)
  y <- round(x + rnorm(20, 0.6, 1.2), 1)
  y[5] <- x[5]                    # one zero difference, dropped by both
  x[c(8, 15)] <- NA
  y[12] <- NA
  wt <- suppressWarnings(wilcox.test(x, y, paired = TRUE, exact = FALSE, correct = TRUE))
  ok <- complete.cases(x, y)
  d <- x[ok] - y[ok]
  n <- sum(d != 0)
  v <- unname(wt$statistic)
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_WILCOXON_SR", "paired_ties_zero_diff_with_nulls",
    list(field = "x", field2 = "y"), pn_rows(x = x, y = y), sum(ok),
    pn_expect(statistic = min(v, n * (n + 1) / 2 - v), p_value = wt$p.value,
              details.w_plus = v, details.n = n, details.zero_diffs = sum(d == 0)))
})

# Summary rows for the one-way posts: group, mean, n, variance; the
# group named `drop` gets a null in `null_col` and leaves the fit.
pn_summary <- function(g, y, drop, null_col) {
  levs <- sort(unique(g))
  m <- tapply(y, g, mean)[levs]
  n <- tapply(y, g, length)[levs]
  v <- tapply(y, g, var)[levs]
  cols <- list(group = levs, mean = unname(m), n = unname(n), variance = unname(v))
  cols[[null_col]][levs == drop] <- NA
  do.call(pn_rows, cols)
}

# ----------------------------------------------------------- TEST_ANOVA_F
local({
  g <- rep(c("a", "b", "c", "d"), times = c(7, 9, 8, 6))
  y <- round(rnorm(length(g), c(a = 5, b = 6.1, c = 5.4, d = 7)[g], 1.3), 2)
  keep <- g != "d"
  gk <- factor(g[keep])
  s <- summary(aov(y[keep] ~ gk))[[1]]
  ssb <- s[1, "Sum Sq"]; ssw <- s[2, "Sum Sq"]
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_ANOVA_F", "summary_rows_one_null_mean",
    list(field = "mean", split_by = "group", params = list(n_col = "n", variance_col = "variance")),
    pn_summary(g, y, "d", "mean"), sum(keep),
    pn_expect(statistic = s[1, "F value"], p_value = s[1, "Pr(>F)"], df = s[1, "Df"],
              details.df_within = s[2, "Df"], details.ss_between = ssb, details.ss_within = ssw,
              details.effect_size.eta_squared = ssb / (ssb + ssw)),
    list(raw_value = lapply(y[keep], num), raw_group = g[keep]))
})

# -------------------------------------------------------- TEST_ANOVA_WELCH
local({
  g <- rep(c("east", "north", "south", "west"), times = c(8, 12, 7, 10))
  y <- round(rnorm(length(g), c(east = 20, north = 22, south = 21, west = 25)[g],
                   c(east = 2, north = 5, south = 1.5, west = 3)[g]), 2)
  keep <- g != "south"
  ow <- oneway.test(y[keep] ~ g[keep], var.equal = FALSE)
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_ANOVA_WELCH", "summary_rows_one_null_variance",
    list(field = "mean", split_by = "group", params = list(n_col = "n", variance_col = "variance")),
    pn_summary(g, y, "south", "variance"), sum(keep),
    pn_expect(statistic = ow$statistic, p_value = ow$p.value, df = ow$parameter[1],
              details.df_within = ow$parameter[2]),
    list(raw_value = lapply(y[keep], num), raw_group = g[keep]))
})

# ---------------------------------------------------------- TEST_TUKEY_HSD
local({
  g <- rep(c("g1", "g2", "g3", "g4", "g5"), times = c(6, 7, 6, 8, 5))
  y <- round(rnorm(length(g), c(g1 = 3, g2 = 3.6, g3 = 4.4, g4 = 3.1, g5 = 9)[g], 0.8), 2)
  keep <- g != "g5"
  tk <- tk_case("unused", g[keep], y[keep])
  # g5's mean is null: the post-test leaves it out of the family (k = 4).
  rows <- c(pn_rows(group = tk$groups, mean = sapply(tk$means, as.numeric), n = sapply(tk$n, as.numeric)),
            pn_rows(group = "g5", mean = NA_real_, n = sum(!keep)))
  ex <- list(details.q_critical = tk$q_critical, details.k_groups = num(length(tk$groups)))
  for (i in seq_along(tk$comparisons)) {
    cmp <- tk$comparisons[[i]]
    for (key in c("diff", "se", "q", "p_adj", "ci_low", "ci_high")) {
      ex[[sprintf("details.comparisons.%d.%s", i - 1, key)]] <- cmp[[key]]
    }
  }
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_TUKEY_HSD", "five_groups_one_null_mean",
    list(field = "mean", split_by = "group", params = list(ms_within = tk$ms_within, df_within = tk$df_within)),
    rows, length(tk$groups), ex)
})

# ----------------------------------------------------- TEST_BROWN_FORSYTHE
local({
  g <- rep(c("a", "b", "c"), times = c(10, 11, 9))
  x <- round(rnorm(length(g), 5, c(a = 1, b = 2.4, c = 0.9)[g]), 2)
  x[c(3, 14)] <- NA
  g[22] <- NA
  ok <- complete.cases(x, g)
  bf <- bf_case("unused", x[ok], g[ok])
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_BROWN_FORSYTHE", "three_groups_null_values_and_label",
    list(field = "value", split_by = "group"), pn_rows(value = x, group = g), sum(ok),
    list(statistic = bf$statistic, p_value = bf$p_value, df = bf$df, details.df_within = bf$df_within,
         details.ss_between = bf$ss_between, details.ss_within = bf$ss_within))
})

# ------------------------------------------------------- TEST_SHAPIRO_WILK
local({
  x <- round(rexp(22, 0.4), 3)
  x[c(1, 9, 16)] <- NA
  sf <- nortest::sf.test(x)
  n <- sum(!is.na(x))
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_SHAPIRO_WILK", "skewed_with_nulls",
    list(field = "value"), pn_rows(value = x), n,
    pn_expect(statistic = sf$statistic, p_value = sf$p.value,
              details.per_group.0.w = sf$statistic, details.per_group.0.p_value = sf$p.value,
              details.per_group.0.n = n))
})

# ------------------------------------------------------------------ TEST_KS
local({
  a <- round(rnorm(20, 0, 1), 3)
  b <- round(rnorm(24, 0.8, 1.2), 3)
  a[c(4, 13)] <- NA
  b[7] <- NA
  d <- unname(ks.test(a, b)$statistic)
  n1 <- sum(!is.na(a)); n2 <- sum(!is.na(b))
  en <- sqrt(n1 * n2 / (n1 + n2))
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_KS", "two_samples_with_nulls",
    list(field = "value", split_by = "group"),
    pn_rows(value = c(a, b), group = rep(c("a", "b"), c(length(a), length(b)))), n1 + n2,
    pn_expect(statistic = d, p_value = kolm_limit((en + 0.12 + 0.11 / en) * d),
              details.n.0 = n1, details.n.1 = n2))
})

# --------------------------------------------------------------- TEST_TREND
local({
  y <- round(0.05 * (1:26) + rnorm(26, 0, 0.5), 1)
  y[c(1, 12)] <- NA
  period <- as.numeric(1:26)
  period[20] <- NA
  ct <- cor.test(period, y, method = "kendall", exact = FALSE, continuity = TRUE)
  ok <- complete.cases(period, y)
  yc <- y[ok]
  n <- length(yc)
  s <- 0
  for (i in 1:(n - 1)) s <- s + sum(sign(yc[(i + 1):n] - yc[i]))
  tt <- table(yc)
  tt <- tt[tt > 1]
  var_s <- (n * (n - 1) * (2 * n + 5) - sum(tt * (tt - 1) * (2 * tt + 5))) / 18
  pn_cases[[length(pn_cases) + 1]] <<- pn_case("TEST_TREND", "series_null_values_and_period",
    list(field = "value", order_by = "period"), pn_rows(period = period, value = y), n,
    pn_expect(statistic = ct$statistic, p_value = ct$p.value,
              details.tau = cor(period, y, method = "kendall", use = "complete.obs"),
              details.s = s, details.var_s = var_s, details.n = n))
})

write_golden("test_post_nulls", c(to_meta(
  "post-test twins over rows holding nulls",
  "per family: cor.test / t.test / wilcox.test / aov / oneway.test / TukeyHSD / anova(lm) / nortest::sf.test / ks.test at R's default NA handling",
  "rows carry null where R reads NA; Pulse drops a row with a null in any used column (listwise) and reports the n it used. expect maps a result path to R's value.",
  list(nortest = as.character(packageVersion("nortest")))
), list(cases = pn_cases)))
