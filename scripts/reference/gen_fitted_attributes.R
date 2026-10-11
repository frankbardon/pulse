# gen_fitted_attributes.R — R oracle for Pulse's FITTED vector attributes
# (U25 E3: ATTR_MAHALANOBIS, ATTR_PC_SCORE, and the factor scores built
# on the same two-pass co-moment fit).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), numx(),
# strs(), near(), write_golden(), mv_meta(), wvec(), n_star(), wcov(),
# wmean(), expand(), sign_fix(), rot_canon() (gen_rotation.R) and the
# fixture data frames (att, mt, wd, nl). Writes
# <out_dir>/mv_mahalanobis.json and <out_dir>/mv_pc_score.json. CI never
# runs R: everything here is committed. Base R (stats) plus the parent's
# pinned psych (the rotated-score cross-check) — no new package pin.
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

# ------------------------------------------------------------ PC scores
# ATTR_PC_SCORE (U25 E3-S2). The fit rows are Mahalanobis's (listwise,
# w > 0, fit_on). M = cov2cor(S) (on = correlation) or S (covariance),
# S = wcov() on the fit rows; eigen(M) with Pulse's sign rule
# (sign_fix, mv_pca.json's construction). Every row complete on the
# members is scored:
#   unrotated  score_c = v_c' D^-1 (x - centre) / sqrt(lambda_c)
#              (D = diag(sqrt(S_ii)) on a correlation, I on a covariance)
#   varimax k  the k unrotated scores %*% rotation, rotation from
#              stats::varimax(A_k) put through rot_canon() (Pulse's
#              order-and-sign rule), A_k = v * sqrt(lambda), k columns.
# Asserted: unit variance (and zero mean) of every score column on the
# fit rows under the weighted ddof = 1 rule; prcomp(scale.) predict()
# / sdev on the unweighted fit (columns sign-aligned: prcomp's signs are
# arbitrary); the frequency fit equals prcomp on the rep()-expanded fit
# rows; and psych::principal(rotate = "varimax")$scores on the
# unweighted correlation fits (columns matched by |r| = 1, sign-aligned).
pc_cases <- list()
align_cols <- function(ref, ours) {
  # For each column of ours, the column of ref it equals up to sign.
  sapply(seq_len(ncol(ours)), function(c) {
    r <- sapply(seq_len(ncol(ref)), function(j) sum(ref[, j] * ours[, c]) / sqrt(sum(ref[, j]^2) * sum(ours[, c]^2)))
    j <- which.max(abs(r))
    if (abs(abs(r[j]) - 1) > 1e-8) stop("align_cols: no column matches")
    j * sign(r[j])
  })
}
pc_case <- function(fx, df, cols, kind, on = "correlation", k = NULL, fit_col = NULL, fit_level = NULL) {
  w <- wvec(df, kind)
  X <- as.matrix(df[, cols, drop = FALSE])
  complete <- stats::complete.cases(X)
  fit <- complete & w > 0
  if (!is.null(fit_col)) fit <- fit & !is.na(df[[fit_col]]) & df[[fit_col]] == fit_level
  Xf <- X[fit, , drop = FALSE]; wf <- w[fit]
  p <- length(cols)
  centre <- wmean(Xf, wf)
  S <- wcov(Xf, wf, kind)
  sdv <- if (on == "correlation") sqrt(diag(S)) else rep(1, p)
  M <- if (on == "correlation") cov2cor(S) else S
  e <- eigen(M, symmetric = TRUE)
  V <- sign_fix(e$vectors); lam <- e$values
  Z <- sweep(sweep(X, 2, centre, "-"), 2, sdv, "/")
  sc <- Z %*% V %*% diag(1 / sqrt(lam), p)
  label <- paste(fx, kind, on, if (is.null(k)) "none" else paste0("varimax", k))
  near(diag(wcov(sc[fit, , drop = FALSE], wf, kind)), rep(1, p), 1e-9, paste("unit variance", label))
  near(wmean(sc[fit, , drop = FALSE], wf), rep(0, p), 1e-9, paste("zero mean", label))
  if (kind != "probability") {
    E <- if (kind == "frequency") as.matrix(expand(as.data.frame(Xf), wf)) else Xf
    pc <- stats::prcomp(E, scale. = (on == "correlation"))
    ps <- sweep(stats::predict(pc, X[complete, , drop = FALSE]), 2, pc$sdev, "/")
    a <- align_cols(ps, sc[complete, , drop = FALSE])
    near(sweep(ps[, abs(a), drop = FALSE], 2, sign(a), "*"), sc[complete, , drop = FALSE], 1e-9, paste("prcomp", label))
  }
  out <- sc
  if (!is.null(k)) {
    A <- sweep(V, 2, sqrt(pmax(lam, 0)), "*")[, seq_len(k), drop = FALSE]
    vm <- stats::varimax(A)
    rc <- rot_canon(vm$loadings, vm$rotmat, diag(k))
    out <- sc[, seq_len(k), drop = FALSE] %*% rc$rotation
    near(diag(wcov(out[fit, , drop = FALSE], wf, kind)), rep(1, k), 1e-9, paste("rotated unit variance", label))
    if (kind == "none" && on == "correlation") {
      pr <- psych::principal(Xf, nfactors = k, rotate = "varimax", scores = TRUE)
      a <- align_cols(pr$scores, out[fit, , drop = FALSE])
      near(sweep(pr$scores[, abs(a), drop = FALSE], 2, sign(a), "*"), out[fit, , drop = FALSE], 1e-8,
           paste("psych::principal varimax", label))
    }
  }
  pc_cases[[length(pc_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), weight = kind, on = on,
    rotation = if (is.null(k)) "none" else "varimax", components = if (is.null(k)) jnull else num(k),
    fit_on = if (is.null(fit_col)) jnull else list(field = fit_col, level = num(fit_level)),
    n_fit = num(sum(fit)), n_star = num(n_star(wf, kind)),
    eigenvalues = numv(lam), scores = numm(out)
  )
}
for (kind in w_kinds) {
  pc_case("attitude", att, names(datasets::attitude), kind)
  pc_case("attitude", att, names(datasets::attitude), kind, k = 3)
  pc_case("attitude", att, names(datasets::attitude), kind, on = "covariance")
  pc_case("mtcars", mt, c("mpg", "disp", "hp", "wt", "qsec"), kind)
  pc_case("mtcars", mt, c("mpg", "disp", "hp", "wt", "qsec"), kind, k = 2, fit_col = "am", fit_level = 0)
  pc_case("weighted", wd, c("x1", "x2", "x3", "x4"), kind)
  pc_case("weighted", wd, c("x1", "x2", "x3", "x4"), kind, on = "covariance", k = 2)
  pc_case("nulls", nl, c("x1", "x2", "x3", "x4"), kind)
}
pc_doc <- c(mv_meta("principal component scores (fitted row score)",
  "eigen(M, symmetric = TRUE) of the fit rows' cov2cor(S) / S; scores = D^-1 (x - centre) %*% v / sqrt(lambda); varimax: stats::varimax(A_k) through rot_canon, scores %*% rotation. Asserted against prcomp predict()/sdev (unweighted and the rep()-expanded frequency fit, sign-aligned), psych::principal(rotate = 'varimax')$scores (unweighted correlation), and unit variance on the fit rows.",
  "Fit rows: complete on every member, w > 0, and (fit_on) fit_on.field == fit_on.level. S = the ddof = 1 covariance on w* (wcov). Eigenvectors carry Pulse's sign rule (largest-magnitude component positive). scores is rows x components: every component (rotation none) or the k rotated (varimax, after Pulse's order-and-sign rule); a row null on any member is null in every column (row order = the fixture's)."),
  list(cases = pc_cases))
write_golden("mv_pc_score", pc_doc)
