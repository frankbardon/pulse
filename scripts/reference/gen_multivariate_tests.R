# gen_multivariate_tests.R — R oracle for Pulse's multivariate TESTS
# (U25: TEST_HOTELLING_T2, and the later profile tests).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), strs(),
# near(), write_golden(), wvec(), n_star(), wcov(), wmean(), expand() and
# the fixture data frames (att, mt, wd). Writes <out_dir>/mv_hotelling.json.
# CI never runs R: everything here is committed.
#
# Pinned packages (refused on any other version, like the parent):
#   Hotelling 1.0.8 (depends on corpcor, pinned by the parent)
# Install: Rscript -e 'install.packages("Hotelling", lib = Sys.getenv("R_LIBS_USER"),
#   repos = "https://cloud.r-project.org")'
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
for (p in names(pinned_tests)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != pinned_tests[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, pinned_tests[[p]]))
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
for (kind in w_kinds) {
  hot_one("attitude", att, c("rating", "complaints", "privileges", "learning"), c(60, 65, 50, 55), kind)
  hot_one("mtcars", mt, c("mpg", "hp", "wt"), c(20, 150, 3), kind)
  hot_one("weighted", wd, c("x1", "x2", "x3"), c(0, 0, 0), kind)
  hot_two("mtcars", mt, c("mpg", "hp", "wt", "qsec"), "am", kind)
  hot_two("weighted", wd, c("x1", "x2", "x3", "x4"), "y_bin", kind)
}
hot_doc <- c(mvt_meta("Hotelling's T-squared",
  "one-sample: closed form ns * t(d) solve(S) d (stats::anova.mlm asserted, unweighted); two-sample: Hotelling::hotelling.test(var.equal = TRUE) on the matrices (unweighted) or on list(mean, cov, n = N*) per level (weighted)",
  "Rows with weight 0 are excluded first (Pulse's row-test rule), so n counts w > 0 rows. N* = sum(w) under none / frequency, Kish n_eff under probability; S = cov.wt (frequency asserted == the rep() expansion; probability method 'unbiased'). One-sample F = (N* - p)/(p (N* - 1)) T2 on (p, N* - p); two-sample pooled S = ((N1*-1) S1 + (N2*-1) S2)/(N1* + N2* - 2), F = (N - p - 1)/(p (N - 2)) T2 on (p, N - p - 1), N = N1* + N2*. Split levels sorted as strings. n_star is per level (two-sample) or a one-entry list."),
  list(cases = hot_cases))
write_golden("mv_hotelling", hot_doc)
