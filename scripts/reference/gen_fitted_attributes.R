# gen_fitted_attributes.R — R oracle for Pulse's FITTED vector attributes
# (U25 E3: ATTR_MAHALANOBIS, ATTR_PC_SCORE, and the factor scores built
# on the same two-pass co-moment fit).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), numx(),
# strs(), near(), write_golden(), mv_meta(), wvec(), n_star(), wcov(),
# wmean(), expand(), sign_fix(), rot_canon() (gen_rotation.R) and the
# fixture data frames (att, mt, wd, nl), and gen_factor.R's fa_quiet(),
# fa_flip(), fa_ml_exact(), fa_ml_loadings() and fa_rotations(). Writes
# <out_dir>/mv_mahalanobis.json, <out_dir>/mv_pc_score.json and
# <out_dir>/mv_factor_score.json. CI never runs R: everything here is
# committed. Base R (stats) plus the parent's pinned psych (the
# rotated-score cross-check and factor.scores) and gen_rotation.R's
# GPArotation — no new package pin.
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

# --------------------------------------------------------- factor scores
# ATTR_FACTOR_SCORE (U25 E3-S3). The fit rows are Mahalanobis's
# (listwise, w > 0, fit_on); R = cov2cor(S), S = wcov() on the fit rows.
# The loadings are MAT_FACTOR's oracle loadings on R (mv_factor.json's
# construction, gen_factor.R): minres = its exact optimum (psych::fa
# fm = "pa", min.err = 1e-14), principal_axis = psych::fa(fm = "pa")
# with psych's defaults, ml = fa_ml_exact + fa_ml_loadings; each put
# through Pulse's rotation definitions and rot_canon (fa_rotations),
# so order and sign are MAT_FACTOR's. The weights are
# psych::factor.scores(R, f = L, Phi = Phi, method = "Thurstone" |
# "Bartlett")$weights (asserted against solve(R, L Phi) and
# U^-2 L (L' U^-2 L)^-1), and every row complete on the members is
# scored Z %*% w, Z = (x - centre) / sqrt(diag(S)). Asserted (unweighted):
# psych::factor.scores(x = fit rows, ...)$scores (scale() of the fit rows)
# and, on the pa-extracted fits, psych::fa(x = fit rows, scores = ...)$scores
# (columns matched by |r| = 1, sign-aligned).
fs_cases <- list()
fs_case <- function(fx, df, cols, kind, k, extraction, rotation = "none", fit_col = NULL, fit_level = NULL, min_u2 = NULL) {
  w <- wvec(df, kind)
  X <- as.matrix(df[, cols, drop = FALSE])
  complete <- stats::complete.cases(X)
  fit <- complete & w > 0
  if (!is.null(fit_col)) fit <- fit & !is.na(df[[fit_col]]) & df[[fit_col]] == fit_level
  Xf <- X[fit, , drop = FALSE]; wf <- w[fit]
  p <- length(cols)
  centre <- wmean(Xf, wf)
  S <- wcov(Xf, wf, kind)
  R <- cov2cor(S)
  label <- paste(fx, kind, extraction, rotation, k)
  converged <- TRUE
  A <- switch(extraction,
    minres = fa_flip(fa_quiet(r = R, nfactors = k, fm = "pa", rotate = "none", min.err = 1e-14, max.iter = 1e6)),
    principal_axis = {
      pa <- fa_quiet(r = R, nfactors = k, fm = "pa", rotate = "none")
      converged <- length(pa$communality.iterations) < 50
      fa_flip(pa)
    },
    ml = fa_ml_loadings(fa_ml_exact(R, k), R, k))
  L <- A; Phi <- diag(k)
  if (rotation != "none" && k >= 2) {
    rot <- fa_rotations(A, k)[[rotation]]
    L <- rot$loadings; Phi <- rot$phi
  }
  Z <- sweep(sweep(X, 2, centre, "-"), 2, sqrt(diag(S)), "/")
  u2 <- 1 - diag(L %*% Phi %*% t(L))
  if (!is.null(min_u2) && min(u2) < min_u2) stop("fixture ", label, " is near-Heywood: min uniqueness ", min(u2), " < ", min_u2)
  one <- function(method) {
    W <- psych::factor.scores(R, f = L, Phi = Phi, method = method)$weights
    W <- unname(as.matrix(W))
    manual <- if (method == "Thurstone") solve(R, L %*% Phi) else
      diag(1 / u2, p) %*% L %*% solve(t(L) %*% diag(1 / u2, p) %*% L)
    near(W, manual, 1e-10, paste("weights", method, label))
    sc <- Z %*% W
    if (kind == "none") {
      ps <- psych::factor.scores(Xf, f = L, Phi = Phi, method = method)$scores
      near(unname(ps), sc[fit, , drop = FALSE], 1e-10, paste("factor.scores on x", method, label))
      if (extraction != "ml") {
        # psych spells GPArotation's geominQ by its own name.
        psych_rot <- if (rotation == "geomin") "geominQ" else rotation
        fa_args <- list(r = Xf, nfactors = k, fm = "pa", rotate = psych_rot, scores = method, n.rotations = 1)
        if (extraction == "minres") fa_args <- c(fa_args, list(min.err = 1e-14, max.iter = 1e6))
        if (rotation != "promax") {
          g <- do.call(fa_quiet, fa_args)
          a <- align_cols(g$scores, sc[fit, , drop = FALSE])
          near(sweep(g$scores[, abs(a), drop = FALSE], 2, sign(a), "*"), sc[fit, , drop = FALSE], 1e-7,
               paste("psych::fa scores", method, label))
        }
      }
    }
    sc
  }
  fs_cases[[length(fs_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), weight = kind, factors = num(k),
    extraction = extraction, rotation = rotation, converged = converged,
    fit_on = if (is.null(fit_col)) jnull else list(field = fit_col, level = num(fit_level)),
    n_fit = num(sum(fit)), loadings = numm(L), phi = numm(Phi), uniquenesses = numv(u2),
    regression = numm(one("Thurstone")), bartlett = numm(one("Bartlett"))
  )
}
for (kind in w_kinds) {
  attn <- names(datasets::attitude)
  fs_case("attitude", att, attn, kind, 2, "minres")
  fs_case("attitude", att, attn, kind, 2, "minres", "varimax")
  fs_case("attitude", att, attn, kind, 3, "principal_axis", "oblimin")
  fs_case("attitude", att, attn, kind, 2, "ml", "promax")
  fs_case("mtcars", mt, c("mpg", "disp", "hp", "wt", "qsec"), kind, 2, "principal_axis", "varimax", fit_col = "am", fit_level = 0)
  fs_case("weighted", wd, c("x1", "x2", "x3", "x4"), kind, 1, "minres")
  fs_case("nulls", nl, c("x1", "x2", "x3", "x4"), kind, 1, "ml")
}
# TODO #410: weighted Bartlett scores on a proper fit. The two_factor
# fixture keeps every uniqueness >= 0.2 under every weight kind and
# extraction (asserted: min_u2), so neither Bartlett nor the Heywood
# rule nulls a score and the frequency / probability Bartlett weights
# U^-2 L (L' U^-2 L)^-1 are pinned on real values, unrotated, orthogonal
# and oblique. Appended after the cases above so their order holds.
for (kind in w_kinds) {
  tfn <- paste0("v", 1:6)
  fs_case("two_factor", tf, tfn, kind, 2, "minres", "varimax", min_u2 = 0.2)
  fs_case("two_factor", tf, tfn, kind, 2, "principal_axis", "oblimin", min_u2 = 0.2)
  fs_case("two_factor", tf, tfn, kind, 2, "ml", min_u2 = 0.2)
}
# matrix-extensions-hardening E6-S2: an oblique GPA criterion beyond
# oblimin (geomin, GPArotation::geominQ at delta 0.01) through the
# scores, on the proper two_factor fit. Appended so the order holds.
for (kind in w_kinds) {
  fs_case("two_factor", tf, paste0("v", 1:6), kind, 2, "principal_axis", "geomin", min_u2 = 0.2)
}
fs_doc <- c(mv_meta("factor scores (fitted row score)",
  "psych::factor.scores(R, f = L, Phi = Phi, method = 'Thurstone' | 'Bartlett')$weights on the fit rows' (weighted) correlation R, L / Phi MAT_FACTOR's oracle loadings (gen_factor.R: exact minres, psych principal axis, exact ML; stats::varimax / stats::promax(m = 4) / GPArotation::oblimin through rot_canon); scores = scale-by-the-fit(x) %*% weights. Asserted against solve(R, L Phi) and U^-2 L (L' U^-2 L)^-1, psych::factor.scores on the fit rows, and psych::fa(scores = ...)$scores (unweighted, principal-axis fits, sign-aligned).",
  "Fit rows: complete on every member, w > 0, and (fit_on) fit_on.field == fit_on.level. S = the ddof = 1 covariance on w* (wcov); R = cov2cor(S); Z = (x - weighted fit mean) / sqrt(diag(S)). loadings / phi are the fitted (rotated) pattern and factor correlation (identity when orthogonal), uniquenesses = 1 - diag(L Phi L'); converged is FALSE when principal axis used all 50 of psych's iterations (its last iterate is the loadings). regression / bartlett are rows x factors (row order = the fixture's); a row null on any member is null in every column. The two_factor cases (TODO #410) are proper fits under every weight kind (every uniqueness >= 0.2, asserted), so their weighted Bartlett scores are real values, never Heywood-nulled."),
  list(cases = fs_cases))
write_golden("mv_factor_score", fs_doc)
