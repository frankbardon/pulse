# gen_fitted_attributes.R — R oracle for Pulse's FITTED vector attributes
# (U25 E3: ATTR_MAHALANOBIS, and the PC / factor scores built on the
# same two-pass co-moment fit).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), numx(),
# strs(), near(), write_golden(), mv_meta(), wvec(), n_star(), wcov(),
# wmean(), expand() and the fixture data frames (att, mt, wd, nl).
# Writes <out_dir>/mv_mahalanobis.json. CI never runs R: everything here
# is committed. Base R (stats) only — no package pin beyond the parent's.
#
# Conventions (Pulse's one weight rule, .claude/reference/weighting.md):
#   * The FIT folds the rows that are complete on every member (listwise),
#     carry a positive weight (w == 0 rows add no mass and are left out)
#     and pass the fit_on selector (fit_col == fit_level when set).
#   * Centre = the weighted mean; S = wcov() — the ddof = 1 covariance on
#     w* (frequency = the rep() expansion, asserted; probability =
#     cov.wt "unbiased"; none = cov()).
#   * EVERY row is scored: D^2 = stats::mahalanobis(x, centre, S) for a
#     row complete on the members (zero-weight and fit_on-excluded rows
#     included), null otherwise; p = pchisq(D^2, df = p, lower.tail =
#     FALSE).

mah_cases <- list()
mah_case <- function(fx, df, cols, kind, fit_col = NULL, fit_level = NULL) {
  w <- wvec(df, kind)
  X <- as.matrix(df[, cols, drop = FALSE])
  complete <- stats::complete.cases(X)
  fit <- complete & w > 0
  if (!is.null(fit_col)) fit <- fit & !is.na(df[[fit_col]]) & df[[fit_col]] == fit_level
  Xf <- X[fit, , drop = FALSE]; wf <- w[fit]
  p <- length(cols)
  centre <- wmean(Xf, wf)
  S <- wcov(Xf, wf, kind)
  ns <- n_star(wf, kind)
  d2 <- rep(NA_real_, nrow(X))
  d2[complete] <- stats::mahalanobis(X[complete, , drop = FALSE], centre, S)
  pv <- stats::pchisq(d2, df = p, lower.tail = FALSE)
  # Independent route: the solve() quadratic form row by row.
  Si <- solve(S)
  for (i in which(complete)) {
    dv <- X[i, ] - centre
    near(as.numeric(t(dv) %*% Si %*% dv), d2[i], 1e-10, paste("solve() D2", fx, i))
  }
  if (kind == "frequency") {
    E <- as.matrix(expand(as.data.frame(Xf), wf))
    near(stats::mahalanobis(X[complete, , drop = FALSE], colMeans(E), cov(E)), d2[complete], 1e-10,
         paste("expansion D2", fx))
  }
  mah_cases[[length(mah_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), weight = kind,
    fit_on = if (is.null(fit_col)) jnull else list(field = fit_col, level = num(fit_level)),
    n_fit = num(sum(fit)), n_star = num(ns), center = numv(centre),
    d2 = numv(d2), p = numv(pv)
  )
}
for (kind in w_kinds) {
  mah_case("attitude", att, c("rating", "complaints", "privileges", "learning"), kind)
  mah_case("mtcars", mt, c("mpg", "disp", "hp", "wt", "qsec"), kind)
  mah_case("mtcars", mt, c("mpg", "hp", "wt"), kind, "am", 0)
  mah_case("weighted", wd, c("x1", "x2", "x3", "x4"), kind)
  mah_case("nulls", nl, c("x1", "x2", "x3", "x4"), kind)
}
mah_doc <- c(mv_meta("Mahalanobis distance (fitted row score)",
  "stats::mahalanobis(x, center, cov) on every row complete on the members, center / cov fitted on the fit rows; p = stats::pchisq(D2, df = p, lower.tail = FALSE). Asserted against the solve() quadratic form per row, and (frequency) against the rep()-expanded fit rows.",
  "The fit rows are complete on every member, carry w > 0 and (fit_on) have fit_on.field == fit_on.level; every row complete on the members is scored, the others are null in d2 / p (row order = the fixture's). center = the weighted mean; cov = the ddof = 1 covariance on w* (none: cov(); frequency: cov.wt ML * N*/(N*-1) == the expansion; probability: cov.wt 'unbiased'). n_fit counts the fit rows, n_star is N* (sum w, or Kish n_eff under probability)."),
  list(cases = mah_cases))
write_golden("mv_mahalanobis", mah_doc)
