# gen_multivariate_tests.R — R oracle for Pulse's multivariate TESTS
# (U25: TEST_HOTELLING_T2, TEST_MANOVA, and the later profile tests).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), strs(),
# near(), write_golden(), wvec(), n_star(), wcov(), wmean(), expand() and
# the fixture data frames (att, mt, wd). Writes <out_dir>/mv_hotelling.json,
# <out_dir>/mv_manova.json and <out_dir>/mv_box_m.json.
# CI never runs R: everything here is committed.
#
# Pinned packages (refused on any other version, like the parent):
#   Hotelling 1.0.8 (depends on corpcor, pinned by the parent)
#   biotools 4.3 (Box's M)
# Install: Rscript -e 'install.packages(c("Hotelling", "biotools"),
#   lib = Sys.getenv("R_LIBS_USER"), repos = "https://cloud.r-project.org")'
#
# Conventions (Pulse's one weight rule, .claude/reference/weighting.md):
#   * A row with weight 0 is EXCLUDED from a test (it never enters n),
#     so every case drops w == 0 rows first; n is the raw row count.
#   * N* = sum(w) (none, frequency) or Kish n_eff (probability); the
#     covariance is wcov() (frequency = the rep() expansion, asserted;
#     probability = cov.wt "unbiased").
#   * Two-sample levels are ordered as strings ("0" < "1"), Pulse's
#     sorted split order; T2 is symmetric in the order anyway.

pinned_tests <- c(Hotelling = "1.0.8")
# biotools rides only mv_box_m.json's packages (box_meta), so the other
# goldens keep their metadata.
pinned_box <- c(biotools = "4.3")
for (pins in list(pinned_tests, pinned_box)) {
  for (p in names(pins)) {
    have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
    if (have != pins[[p]]) {
      stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, pins[[p]]))
    }
  }
}
suppressPackageStartupMessages(library(Hotelling))

mvt_meta <- function(family, r_fn, note) {
  m <- mv_meta(family, r_fn, note)
  m$packages <- c(m$packages, lapply(as.list(pinned_tests), function(v) v))
  m
}

# ----------------------------------------------------------- Hotelling T2
hot_cases <- list()
hot_one <- function(fx, df, cols, mu, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  X <- as.matrix(d[, cols]); p <- length(cols)
  ns <- n_star(w, kind)
  m <- wmean(X, w)
  S <- wcov(X, w, kind)
  dv <- m - mu
  T2 <- as.numeric(ns * t(dv) %*% solve(S) %*% dv)
  F <- (ns - p) / (p * (ns - 1)) * T2
  df2 <- ns - p
  pv <- pf(F, p, df2, lower.tail = FALSE)
  if (kind == "none") {
    # Independent route: the intercept test of an intercept-only mlm on
    # x - mu (stats::anova.mlm, Hotelling-Lawley = T2 / (n - 1)).
    fit <- lm(sweep(X, 2, mu) ~ 1)
    a <- anova(fit, test = "Hotelling-Lawley")
    near(a$`approx F`[1], F, 1e-10, paste("anova.mlm F", fx))
    near(a$`Pr(>F)`[1], pv, 1e-10, paste("anova.mlm p", fx))
  }
  if (kind == "frequency") {
    E <- as.matrix(expand(d, w)[, cols])
    ne <- nrow(E); Se <- cov(E); de <- colMeans(E) - mu
    near(ne * t(de) %*% solve(Se) %*% de, T2, 1e-10, paste("expansion T2", fx))
  }
  hot_cases[[length(hot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), split = jnull, mu = numv(mu), weight = kind,
    n = num(nrow(X)), n_star = numv(ns), mean = numv(m),
    t2 = num(T2), f = num(F), df1 = num(p), df2 = num(df2), p_value = num(pv)
  )
}
hot_two <- function(fx, df, cols, split, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  lv <- sort(as.character(unique(d[[split]])))
  if (length(lv) != 2) stop("hot_two: ", fx, " ", split, " needs 2 levels")
  p <- length(cols)
  part <- lapply(lv, function(l) {
    k <- as.character(d[[split]]) == l
    X <- as.matrix(d[k, cols]); wk <- w[k]
    list(X = X, w = wk, n = nrow(X), ns = n_star(wk, kind), m = wmean(X, wk), S = wcov(X, wk, kind))
  })
  a <- part[[1]]; b <- part[[2]]
  if (kind == "none") {
    r <- hotelling.test(a$X, b$X)
  } else {
    r <- hotelling.test(list(mean = a$m, cov = a$S, n = a$ns), list(mean = b$m, cov = b$S, n = b$ns))
  }
  T2 <- r$stats$statistic
  F <- r$stats$m * T2
  df2 <- r$stats$df[2]
  # Hotelling's pval is 1 - pf(), which cancels to 0 in the far tail;
  # the golden takes the upper tail directly and asserts the package's
  # value to 1e-10 absolute.
  pv <- pf(F, p, df2, lower.tail = FALSE)
  near(r$pval, pv, 1e-10, paste("Hotelling pval", fx))
  if (kind == "frequency") {
    Ea <- as.matrix(expand(as.data.frame(a$X), a$w)); Eb <- as.matrix(expand(as.data.frame(b$X), b$w))
    re <- hotelling.test(Ea, Eb)
    near(re$stats$statistic, T2, 1e-10, paste("expansion T2", fx))
  }
  hot_cases[[length(hot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), split = split, levels = strs(lv), mu = jnull, weight = kind,
    n = numv(c(a$n, b$n)), n_star = numv(c(a$ns, b$ns)),
    t2 = num(T2), f = num(F), df1 = num(p), df2 = num(df2), p_value = num(pv)
  )
}
# Paired (U25 E1-S2): the one-sample closed form on the row-wise
# difference vector D = X - Y against mu. Unweighted, asserted against
# stats::anova.mlm on D - mu (p > 1) or stats::t.test(paired) (p = 1).
hot_paired <- function(fx, df, cols, cols2, mu, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  D <- as.matrix(d[, cols]) - as.matrix(d[, cols2]); p <- length(cols)
  ns <- n_star(w, kind)
  m <- wmean(D, w)
  S <- wcov(D, w, kind)
  dv <- m - mu
  T2 <- as.numeric(ns * t(dv) %*% solve(S) %*% dv)
  F <- (ns - p) / (p * (ns - 1)) * T2
  df2 <- ns - p
  pv <- pf(F, p, df2, lower.tail = FALSE)
  if (kind == "none") {
    if (p == 1) {
      tt <- t.test(d[[cols]], d[[cols2]], paired = TRUE, mu = mu)
      near(unname(tt$statistic)^2, T2, 1e-10, paste("paired t.test t^2", fx))
      near(tt$p.value, pv, 1e-10, paste("paired t.test p", fx))
    } else {
      a <- anova(lm(sweep(D, 2, mu) ~ 1), test = "Hotelling-Lawley")
      near(a$`approx F`[1], F, 1e-10, paste("paired anova.mlm F", fx))
      near(a$`Pr(>F)`[1], pv, 1e-10, paste("paired anova.mlm p", fx))
    }
  }
  if (kind == "frequency") {
    E <- as.matrix(expand(as.data.frame(D), w))
    ne <- nrow(E); de <- colMeans(E) - mu
    near(ne * t(de) %*% solve(cov(E)) %*% de, T2, 1e-10, paste("paired expansion T2", fx))
  }
  hot_cases[[length(hot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), fields2 = strs(cols2), split = jnull, mu = numv(mu), weight = kind,
    form = "paired", n = num(nrow(D)), n_star = numv(ns), mean_diff = numv(m),
    t2 = num(T2), f = num(F), df1 = num(p), df2 = num(df2), p_value = num(pv)
  )
}
# Unequal covariances (U25 E1-S2): Krishnamoorthy & Yu (2004), the
# modified Nel-Van der Merwe test, hand-computed (MVTests::TwoSamplesHT2
# does not install on R 4.6.1; Hotelling::hotelling.test(var.equal =
# FALSE) is the ORIGINAL Nel-Van der Merwe df with the pooled F scale,
# not this test). V_i = S_i / N*_i, St = V_1 + V_2, T2 = d' St^-1 d,
# nu = (p + p^2) / sum_i (tr[(V_i St^-1)^2] + tr[V_i St^-1]^2) / (N*_i - 1),
# F = (nu - p + 1) / (nu p) T2 on (p, nu - p + 1). Asserted: the T2
# equals Hotelling's var.equal = FALSE statistic (same St), and at p = 1
# the test is Welch's (stats::t.test(var.equal = FALSE): t^2, df, p).
ky_test <- function(a, b, p) {
  tr <- function(M) sum(diag(M))
  Va <- a$S / a$ns; Vb <- b$S / b$ns; St <- Va + Vb; Si <- solve(St)
  dv <- a$m - b$m
  T2 <- as.numeric(t(dv) %*% Si %*% dv)
  Aa <- Va %*% Si; Ab <- Vb %*% Si
  nu <- (p + p^2) / ((tr(Aa %*% Aa) + tr(Aa)^2) / (a$ns - 1) + (tr(Ab %*% Ab) + tr(Ab)^2) / (b$ns - 1))
  df2 <- nu - p + 1
  F <- df2 / (nu * p) * T2
  list(T2 = T2, F = F, df2 = df2, p = pf(F, p, df2, lower.tail = FALSE))
}
hot_unequal <- function(fx, df, cols, split, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  lv <- sort(as.character(unique(d[[split]])))
  if (length(lv) != 2) stop("hot_unequal: ", fx, " ", split, " needs 2 levels")
  p <- length(cols)
  part <- lapply(lv, function(l) {
    k <- as.character(d[[split]]) == l
    X <- as.matrix(d[k, cols, drop = FALSE]); wk <- w[k]
    list(X = X, w = wk, n = nrow(X), ns = n_star(wk, kind), m = wmean(X, wk), S = wcov(X, wk, kind))
  })
  a <- part[[1]]; b <- part[[2]]
  r <- ky_test(a, b, p)
  hs <- hotelling.stat(list(mean = a$m, cov = a$S, n = a$ns), list(mean = b$m, cov = b$S, n = b$ns), var.equal = FALSE)
  near(hs$statistic, r$T2, 1e-10, paste("Hotelling var.equal=FALSE T2", fx))
  if (kind == "none" && p == 1) {
    tt <- t.test(a$X[, 1], b$X[, 1], var.equal = FALSE)
    near(unname(tt$statistic)^2, r$T2, 1e-10, paste("Welch t^2", fx))
    near(unname(tt$parameter), r$df2, 1e-10, paste("Welch df", fx))
    near(tt$p.value, r$p, 1e-10, paste("Welch p", fx))
  }
  if (kind == "frequency") {
    ex <- function(z) {
      E <- as.matrix(expand(as.data.frame(z$X), z$w))
      list(ns = nrow(E), m = colMeans(E), S = cov(E))
    }
    re <- ky_test(ex(a), ex(b), p)
    near(re$T2, r$T2, 1e-10, paste("unequal expansion T2", fx))
    near(re$df2, r$df2, 1e-10, paste("unequal expansion df2", fx))
  }
  hot_cases[[length(hot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), split = split, levels = strs(lv), mu = jnull, weight = kind,
    form = "two_sample_unequal", variance = "unequal",
    n = numv(c(a$n, b$n)), n_star = numv(c(a$ns, b$ns)),
    t2 = num(r$T2), f = num(r$F), df1 = num(p), df2 = num(r$df2), p_value = num(r$p)
  )
}
for (kind in w_kinds) {
  hot_one("attitude", att, c("rating", "complaints", "privileges", "learning"), c(60, 65, 50, 55), kind)
  hot_one("mtcars", mt, c("mpg", "hp", "wt"), c(20, 150, 3), kind)
  hot_one("weighted", wd, c("x1", "x2", "x3"), c(0, 0, 0), kind)
  hot_two("mtcars", mt, c("mpg", "hp", "wt", "qsec"), "am", kind)
  hot_two("weighted", wd, c("x1", "x2", "x3", "x4"), "y_bin", kind)
  hot_paired("attitude", att, c("rating", "complaints"), c("privileges", "learning"), c(0, 0), kind)
  hot_paired("attitude", att, c("rating", "complaints", "raises"), c("privileges", "learning", "advance"), c(10, 15, 20), kind)
  hot_paired("mtcars", mt, "qsec", "mpg", 0, kind)
  hot_paired("weighted", wd, c("x1", "x2"), c("x3", "x4"), c(0, 0), kind)
  hot_unequal("mtcars", mt, c("mpg", "hp", "wt", "qsec"), "am", kind)
  hot_unequal("mtcars", mt, "mpg", "am", kind)
  hot_unequal("weighted", wd, c("x1", "x2", "x3", "x4"), "y_bin", kind)
}
hot_doc <- c(mvt_meta("Hotelling's T-squared",
  "one-sample: closed form ns * t(d) solve(S) d (stats::anova.mlm asserted, unweighted); two-sample: Hotelling::hotelling.test(var.equal = TRUE) on the matrices (unweighted) or on list(mean, cov, n = N*) per level (weighted); paired (form = paired): the one-sample closed form on D = fields - fields2 (stats::anova.mlm, or stats::t.test(paired) at p = 1, asserted unweighted); unequal (form = two_sample_unequal): Krishnamoorthy-Yu (2004) hand-computed, T2 asserted against Hotelling::hotelling.stat(var.equal = FALSE), Welch stats::t.test asserted at p = 1",
  "Rows with weight 0 are excluded first (Pulse's row-test rule), so n counts w > 0 rows. N* = sum(w) under none / frequency, Kish n_eff under probability; S = cov.wt (frequency asserted == the rep() expansion; probability method 'unbiased'). One-sample F = (N* - p)/(p (N* - 1)) T2 on (p, N* - p); two-sample pooled S = ((N1*-1) S1 + (N2*-1) S2)/(N1* + N2* - 2), F = (N - p - 1)/(p (N - 2)) T2 on (p, N - p - 1), N = N1* + N2*. Split levels sorted as strings. n_star is per level (two-sample) or a one-entry list. Paired: one-sample on the difference vector, F on (p, N* - p). Unequal: V_i = S_i / N*_i, St = V_1 + V_2, T2 = d' St^-1 d, nu = (p + p^2) / sum_i (tr[(V_i St^-1)^2] + tr[V_i St^-1]^2) / (N*_i - 1), F = (nu - p + 1)/(nu p) T2 on (p, nu - p + 1). A case without `form` is one_sample (split null) or two_sample_pooled."),
  list(cases = hot_cases))
write_golden("mv_hotelling", hot_doc)

# ----------------------------------------------------------- MANOVA
# TEST_MANOVA (U25 E1-S3): one-way MANOVA over a split_by. Per level
# N*_g (sum w or Kish n_eff) and M2*_g = c_g * M2_g with the level's OWN
# c_g = N*_g / sum(w_g) (TEST_ANOVA_F's rule); E = sum_g M2*_g,
# grand mean = sum_g N*_g m_g / N*, H = sum_g N*_g (m_g - gm)(m_g - gm)',
# q = k - 1, df.res = N* - k. The four statistics are stats:::Pillai /
# Wilks / HL / Roy on eigen(E^-1 H) with summary.manova's p rule (p only
# when F >= 0 and both df > 0). Asserted: unweighted against
# summary(manova(X ~ g), test = ...) for all four; frequency against the
# same on the rep() expansion; E + H equals the merged (total) SSCP
# under none / frequency. Probability is the one rule hand-computed.
man_cases <- list()
man_stat <- function(eig, q, dfr, fn) {
  r <- fn(eig, q, dfr)
  ok <- !is.na(r[2]) && r[2] >= 0 && r[3] > 0 && r[4] > 0
  list(statistic = num(r[1]), f = num(r[2]), df1 = num(r[3]), df2 = num(r[4]),
       p_value = if (ok) num(pf(r[2], r[3], r[4], lower.tail = FALSE)) else jnull,
       raw = c(r, if (ok) pf(r[2], r[3], r[4], lower.tail = FALSE) else NA))
}
man_r_tests <- c(pillai = "Pillai", wilks = "Wilks", hotelling_lawley = "Hotelling-Lawley", roy = "Roy")
man_assert <- function(X, g, st, what) {
  fit <- manova(X ~ g)
  for (nm in names(man_r_tests)) {
    s <- summary(fit, test = man_r_tests[[nm]])$stats[1, 2:6]
    near(unname(s), st[[nm]]$raw, 1e-10, paste("summary.manova", man_r_tests[[nm]], what))
  }
}
man_case <- function(fx, df, cols, split, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  lv <- sort(as.character(unique(d[[split]])))
  k <- length(lv); p <- length(cols)
  part <- lapply(lv, function(l) {
    kk <- as.character(d[[split]]) == l
    X <- as.matrix(d[kk, cols, drop = FALSE]); wk <- w[kk]
    ns <- n_star(wk, kind)
    m <- wmean(X, wk)
    Xc <- sweep(X, 2, m)
    list(n = nrow(X), ns = ns, m = m, M2 = crossprod(Xc * sqrt(wk)) * ns / sum(wk))
  })
  ns <- sapply(part, `[[`, "ns"); N <- sum(ns)
  E <- Reduce(`+`, lapply(part, `[[`, "M2"))
  gm <- Reduce(`+`, lapply(part, function(z) z$ns * z$m)) / N
  H <- Reduce(`+`, lapply(part, function(z) z$ns * tcrossprod(z$m - gm)))
  q <- k - 1; dfr <- N - k
  eig <- Re(eigen(solve(E) %*% H, only.values = TRUE)$values)
  st <- list(pillai = man_stat(eig, q, dfr, stats:::Pillai), wilks = man_stat(eig, q, dfr, stats:::Wilks),
             hotelling_lawley = man_stat(eig, q, dfr, stats:::HL), roy = man_stat(eig, q, dfr, stats:::Roy))
  X <- as.matrix(d[, cols]); g <- factor(as.character(d[[split]]), levels = lv)
  if (kind != "probability") {
    tm <- wmean(X, w)
    Tt <- crossprod(sweep(X, 2, tm) * sqrt(w))
    near(E + H, Tt, 1e-10, paste("E + H = total SSCP", fx))
  }
  if (kind == "none") man_assert(X, g, st, fx)
  if (kind == "frequency") {
    r <- rep(seq_len(nrow(X)), w)
    man_assert(X[r, , drop = FALSE], g[r], st, paste("expansion", fx))
  }
  out <- lapply(st, function(z) z[c("statistic", "f", "df1", "df2", "p_value")])
  man_cases[[length(man_cases) + 1]] <<- c(list(
    fixture = fx, fields = strs(cols), split = split, levels = strs(lv), weight = kind,
    n = numv(sapply(part, `[[`, "n")), n_star = numv(ns),
    df_hypothesis = num(q), df_residual = num(dfr), eigenvalues = numv(eig)), out)
}
for (kind in w_kinds) {
  man_case("mtcars", mt, c("mpg", "hp", "wt", "qsec"), "cyl", kind)
  man_case("mtcars", mt, c("mpg", "disp", "drat"), "gear", kind)
  man_case("mtcars", mt, c("mpg", "wt"), "carb", kind)
  man_case("weighted", wd, c("x1", "x2", "x3", "x4"), "y_bin", kind)
}
man_doc <- c(mvt_meta("one-way MANOVA",
  "stats:::Pillai / Wilks / HL / Roy on eigen(solve(E) %*% H); summary(manova(X ~ g), test = Pillai | Wilks | Hotelling-Lawley | Roy) asserted unweighted and on the rep() expansion (frequency)",
  "Rows with weight 0 are excluded first; n counts w > 0 rows per level. Per level N*_g = sum(w) (none / frequency) or Kish n_eff (probability) and M2*_g = (N*_g / sum(w_g)) * sum w (x - m_g)(x - m_g)'. E = sum_g M2*_g; gm = sum_g N*_g m_g / N*; H = sum_g N*_g (m_g - gm)(m_g - gm)'; q = k - 1 (df_hypothesis), df.res = N* - k (df_residual). eigenvalues: all p of E^-1 H, descending (R's order). Levels sorted as strings. p_value is null when R's summary.manova would print NA (F < 0 or a df <= 0)."),
  list(cases = man_cases))
write_golden("mv_manova", man_doc)

# ----------------------------------------------------------- Box's M
# TEST_BOX_M (U25 E1-S4): equality of covariance matrices across a
# split_by. Per level N*_g, M2*_g = c_g * M2_g (the MANOVA fold),
# nu_g = N*_g - 1, S_g = M2*_g / nu_g, S_p = sum M2*_g / sum nu_g;
# M = sum(nu) log|S_p| - sum nu_g log|S_g|; A1 = (2p^2 + 3p - 1) /
# (6 (p + 1) (k - 1)) (sum 1/nu_g - 1/sum nu); chi2 = M (1 - A1) on
# p (p + 1) / 2 (k - 1) df. Box's F approximation (SPSS's form): A2 =
# (p - 1)(p + 2) / (6 (k - 1)) (sum 1/nu_g^2 - 1/(sum nu)^2), df1 = the
# chi2 df; A2 > A1^2: df2 = (df1 + 2)/(A2 - A1^2), b = df1/(1 - A1 -
# df1/df2), F = M/b; A2 < A1^2: df2 = (df1 + 2)/(A1^2 - A2), b = df2/(1
# - A1 + 2/df2), F = df2 M/(df1 (b - M)). Asserted: chi2 / df / p against
# biotools::boxM unweighted and on the rep() expansion (frequency); the
# F form reproduces SPSS's iris figures (F = 7.045, df2 = 77566.75).
# Probability is the one rule hand-computed.
box_f <- function(M, A1, A2, df1) {
  d <- A2 - A1^2
  if (d > 0) {
    df2 <- (df1 + 2) / d; b <- df1 / (1 - A1 - df1 / df2); F <- M / b
  } else {
    df2 <- (df1 + 2) / -d; b <- df2 / (1 - A1 + 2 / df2); F <- df2 * M / (df1 * (b - M))
  }
  list(f = F, df2 = df2, fp = pf(F, df1, df2, lower.tail = FALSE), branch = if (d > 0) "a2_gt" else "a2_lt")
}
box_stats <- function(parts, p) {
  k <- length(parts)
  nu <- sapply(parts, `[[`, "ns") - 1
  Sp <- Reduce(`+`, lapply(parts, `[[`, "M2")) / sum(nu)
  ld <- mapply(function(z, v) as.numeric(determinant(z$M2 / v)$modulus), parts, nu)
  ldp <- as.numeric(determinant(Sp)$modulus)
  M <- sum(nu) * ldp - sum(nu * ld)
  A1 <- (2 * p^2 + 3 * p - 1) / (6 * (p + 1) * (k - 1)) * (sum(1 / nu) - 1 / sum(nu))
  A2 <- (p - 1) * (p + 2) / (6 * (k - 1)) * (sum(1 / nu^2) - 1 / sum(nu)^2)
  df1 <- p * (p + 1) / 2 * (k - 1)
  chi <- M * (1 - A1)
  c(list(m = M, chisq = chi, df = df1, p = pchisq(chi, df1, lower.tail = FALSE), log_det = ld, log_det_pooled = ldp),
    box_f(M, A1, A2, df1))
}
local({
  X <- as.matrix(iris[, 1:4]); g <- as.character(iris$Species)
  parts <- lapply(sort(unique(g)), function(l) {
    Z <- X[g == l, ]; list(ns = nrow(Z), M2 = cov(Z) * (nrow(Z) - 1))
  })
  st <- box_stats(parts, 4)
  near(st$f, 7.045262, 1e-6, "SPSS iris Box F")
  near(st$df2, 77566.75, 1e-6, "SPSS iris Box df2")
})
box_cases <- list()
box_case <- function(fx, df, cols, split, kind) {
  w <- wvec(df, kind)
  d <- df[w > 0, , drop = FALSE]; w <- w[w > 0]
  lv <- sort(as.character(unique(d[[split]])))
  p <- length(cols)
  part <- lapply(lv, function(l) {
    kk <- as.character(d[[split]]) == l
    X <- as.matrix(d[kk, cols, drop = FALSE]); wk <- w[kk]
    ns <- n_star(wk, kind)
    m <- wmean(X, wk)
    list(n = nrow(X), ns = ns, M2 = crossprod(sweep(X, 2, m) * sqrt(wk)) * ns / sum(wk))
  })
  st <- box_stats(part, p)
  X <- as.matrix(d[, cols, drop = FALSE]); g <- as.character(d[[split]])
  chk <- function(X, g, what) {
    if (p == 1) return(invisible())  # biotools::boxM needs a matrix of >= 2 columns
    b <- suppressWarnings(biotools::boxM(X, g))
    near(unname(b$statistic), st$chisq, 1e-10, paste("biotools::boxM chi2", what))
    near(unname(b$parameter), st$df, 1e-12, paste("biotools::boxM df", what))
    near(b$p.value, st$p, 1e-10, paste("biotools::boxM p", what))
  }
  if (kind == "none") chk(X, g, fx)
  if (kind == "frequency") {
    r <- rep(seq_len(nrow(X)), w)
    chk(X[r, , drop = FALSE], g[r], paste("expansion", fx))
  }
  box_cases[[length(box_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), split = split, levels = strs(lv), weight = kind,
    n = numv(sapply(part, `[[`, "n")), n_star = numv(sapply(part, `[[`, "ns")),
    m = num(st$m), chisq = num(st$chisq), df = num(st$df), p_value = num(st$p),
    f = num(st$f), df2 = num(st$df2), f_p_value = num(st$fp), f_branch = st$branch,
    log_det = numv(st$log_det), log_det_pooled = num(st$log_det_pooled))
}
for (kind in w_kinds) {
  box_case("mtcars", mt, c("mpg", "hp", "wt"), "cyl", kind)
  box_case("mtcars", mt, c("mpg", "wt", "qsec"), "am", kind)
  box_case("mtcars", mt, "mpg", "cyl", kind)
  box_case("weighted", wd, c("x1", "x2", "x3", "x4"), "y_bin", kind)
}
box_meta <- function(family, r_fn, note) {
  m <- mvt_meta(family, r_fn, note)
  m$packages <- c(m$packages, lapply(as.list(pinned_box), function(v) v))
  m
}
box_doc <- c(box_meta("Box's M",
  "biotools::boxM (chi-square approximation) asserted unweighted and on the rep() expansion (frequency; p >= 2); Box's F approximation hand-computed in SPSS's form, checked against SPSS's iris figures",
  "Rows with weight 0 are excluded first; n counts w > 0 rows per level. Per level N*_g = sum(w) (none / frequency) or Kish n_eff (probability), M2*_g = (N*_g / sum(w_g)) * sum w (x - m_g)(x - m_g)', nu_g = N*_g - 1, S_g = M2*_g / nu_g, S_p = sum M2*_g / sum nu_g. m = sum(nu) log|S_p| - sum nu_g log|S_g|; chisq = m (1 - A1) on df = p (p + 1)/2 (k - 1). f / df2 / f_p_value: Box's F on (df, df2); f_branch names the A2 vs A1^2 branch (p = 1 is always a2_lt). log_det per level (sorted) and log_det_pooled are natural logs. Levels sorted as strings."),
  list(cases = box_cases))
write_golden("mv_box_m", box_doc)
