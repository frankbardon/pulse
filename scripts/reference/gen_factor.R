# gen_factor.R — R oracle for Pulse's MAT_FACTOR (U25 E2-S2: exploratory
# factor analysis by minres and principal-axis extraction, then the
# shared rotation engine).
#
# Sourced by gen_multivariate.R (`make reference`) after gen_rotation.R:
# it reuses out_dir, num(), numx(), numv(), numm(), strs(), near(),
# write_golden(), mv_meta(), wvec(), wcov(), n_star(), listwise(),
# rot_canon(), pinned_rot and the fixture data frames (att, mt, lk, wd,
# hw). Writes <out_dir>/mv_factor.json. CI never runs R: everything is
# committed.
#
# Pinned packages: the parent's psych 2.6.9 and gen_rotation.R's
# GPArotation 2026.8.2 (both refused on any other version upstream).
#
# Per case (fixture, weight kind, k):
#   * R (correlation) is the fixture's listwise (weighted) correlation, n_star the
#     weighting.md inference size (N, sum w, Kish n_eff).
#   * minres: psych::fa(R, k, fm = "minres", rotate = "none",
#     n.obs = n_star). psych's L-BFGS-B stops early (default factr; its
#     FAgr gradient is the ULS one), so it is ALSO recorded at the exact
#     optimum: the minres optimum is the principal-axis fixed point
#     (diag residual zero), reached by psych::fa(fm = "pa",
#     min.err = 1e-14, max.iter = 1e6) — "exact".
#   * principal_axis: psych::fa(R, k, fm = "pa", rotate = "none",
#     n.obs = n_star) with psych's defaults (SMC start, min.err 0.001,
#     max.iter 50); iterations = length(communality.iterations).
#   * Each extraction's unrotated loadings (psych's column flip to a
#     positive sum) are rotated with Pulse's engine definitions —
#     stats::varimax, stats::promax(m = 4), GPArotation::oblimin(gam = 0)
#     — and put through rot_canon (gen_rotation.R). psych::fa's own
#     varimax and oblimin make the same calls and are asserted equal;
#     psych::fa's "promax" is kaiser() around GPArotation::Varimax, NOT
#     stats::promax, so it is not used.
#   * rmsr = sqrt(sum_{i<j} res^2 / (p (p - 1))), empirical_chisq =
#     sum_{i<j} res^2 * n_star, df = p(p-1)/2 - p k + k(k-1)/2,
#     empirical_p its upper tail (null when df <= 0): psych's fa.stats
#     rms / chi / dof / EPVAL, asserted. objective is the off-diagonal
#     residual SS sum_{i != j} res^2.
#   * kmo = psych::KMO(R)$MSA; bartlett = psych::cortest.bartlett(R,
#     n = n_star).

fa_quiet <- function(...) {
  suppressWarnings(suppressMessages(psych::fa(...)))
}

fa_stats <- function(R, L, ns) {
  p <- ncol(R); k <- ncol(L)
  res <- R - L %*% t(L)
  off <- sum(res[lower.tri(res)]^2)
  df <- p * (p - 1) / 2 - p * k + k * (k - 1) / 2
  list(rmsr = sqrt(off / (p * (p - 1))), chisq = off * ns, df = df,
       p = if (df > 0) pchisq(off * ns, df, lower.tail = FALSE) else NA_real_,
       objective = 2 * off)
}

fa_rotations <- function(A, k) {
  if (k < 2) return(NULL)
  vm <- stats::varimax(A)
  pm <- stats::promax(A, m = 4)
  ui <- solve(pm$rotmat)
  ob <- GPArotation::oblimin(A, gam = 0)
  if (!ob$convergence) stop("oblimin did not converge")
  orot <- t(solve(ob$Th))
  list(
    varimax = rot_canon(vm$loadings, vm$rotmat, diag(k)),
    promax = rot_canon(pm$loadings, pm$rotmat, ui %*% t(ui)),
    oblimin = rot_canon(ob$loadings, orot, ob$Phi)
  )
}
fa_enc_rot <- function(rot) {
  lapply(rot, function(r) list(loadings = numm(r$loadings), phi = numm(r$phi), variance = numv(r$variance)))
}

fa_flip <- function(f) {
  L <- unname(unclass(f$loadings))
  L <- matrix(as.numeric(L), nrow = nrow(L))
  L
}

factor_cases <- list()
add_factor <- function(fx, df, cols, kind, k, exact = TRUE) {
  d <- listwise(df, cols)
  X <- as.matrix(d[, cols])
  w <- wvec(d, kind)
  ns <- n_star(w, kind)
  R <- cov2cor(wcov(X, w, kind))
  dimnames(R) <- list(cols, cols)
  out <- list(fixture = fx, fields = strs(cols), weight = kind, k = num(k), n_star = num(ns), correlation = numm(R),
              smc = numv(psych::smc(R)), kmo = num(psych::KMO(R)$MSA))
  bt <- psych::cortest.bartlett(R, n = ns)
  out$bartlett_chisq <- num(bt$chisq); out$bartlett_df <- num(bt$df); out$bartlett_p <- num(bt$p.value)

  one <- function(f, A, check_stats = TRUE) {
    st <- fa_stats(R, A, ns)
    if (check_stats) {
      near(st$rmsr, f$rms, 1e-10, paste("rms", fx))
      near(st$chisq, f$chi, 1e-10, paste("chi", fx))
      near(st$df, f$dof, 0, paste("dof", fx))
      if (st$df > 0) near(st$p, f$EPVAL, 1e-10, paste("EPVAL", fx))
    }
    h <- rowSums(A^2)
    near(h, f$communality, 1e-12, paste("communality", fx))
    near(1 - h, f$uniquenesses, 1e-12, paste("uniquenesses", fx))
    e <- list(unrotated = numm(A), communalities = numv(h), uniquenesses = numv(1 - h),
              variance = numv(colSums(A^2)),
              rmsr = num(st$rmsr), empirical_chisq = num(st$chisq), df = num(st$df),
              empirical_p = numx(st$p), objective = num(st$objective))
    rot <- fa_rotations(A, k)
    if (!is.null(rot)) e$rotations <- fa_enc_rot(rot)
    if (k >= 2 && check_stats) {
      # psych::fa's own varimax and oblimin (single start, n.rotations = 1;
      # its default 20 random starts are not reproducible) are the same calls.
      for (m in c("varimax", "oblimin")) {
        g <- fa_quiet(r = R, nfactors = k, fm = f$fm, rotate = m, n.obs = ns, n.rotations = 1)
        Phi <- if (is.null(g$Phi)) diag(k) else g$Phi
        want <- rot_canon(g$loadings, diag(k), Phi)
        near(want$loadings, rot[[m]]$loadings, 1e-8, paste("psych", m, fx))
      }
    }
    e
  }

  mr <- fa_quiet(r = R, nfactors = k, fm = "minres", rotate = "none", n.obs = ns)
  out$minres <- one(mr, fa_flip(mr))
  if (exact) {
    ex <- fa_quiet(r = R, nfactors = k, fm = "pa", rotate = "none", n.obs = ns, min.err = 1e-14, max.iter = 1e6)
    e <- one(ex, fa_flip(ex), check_stats = FALSE)
    e$iterations <- num(length(ex$communality.iterations))
    if (length(ex$communality.iterations) >= 1e6) stop("exact minres did not converge on ", fx)
    out$minres_exact <- e
  }
  pa <- fa_quiet(r = R, nfactors = k, fm = "pa", rotate = "none", n.obs = ns)
  e <- one(pa, fa_flip(pa))
  e$iterations <- num(length(pa$communality.iterations))
  e$converged <- length(pa$communality.iterations) < 50
  out$principal_axis <- e
  factor_cases[[length(factor_cases) + 1]] <<- out
}
add_factor("attitude", att, names(datasets::attitude), "none", 1)
add_factor("attitude", att, names(datasets::attitude), "none", 2)
add_factor("attitude", att, names(datasets::attitude), "none", 3)
add_factor("attitude", att, names(datasets::attitude), "probability", 2)
add_factor("mtcars", mt, names(datasets::mtcars), "none", 2)
add_factor("mtcars", mt, names(datasets::mtcars), "none", 3)
add_factor("mtcars", mt, names(datasets::mtcars), "frequency", 3)
add_factor("likert_ties", lk, paste0("q", 1:6), "none", 2)
add_factor("weighted", wd, paste0("x", 1:4), "frequency", 1)
add_factor("heywood", hw, c("h1", "h2", "h3"), "none", 1, exact = FALSE)

factor_meta <- mv_meta("exploratory factor analysis (minres, principal axis) with rotation",
  "psych::fa(r = R, nfactors = k, fm = 'minres' | 'pa', rotate = 'none', n.obs = n_star); stats::varimax, stats::promax(m = 4), GPArotation::oblimin(gam = 0) on the unrotated loadings",
  "R = the fixture's listwise (weighted) correlation; n_star = N / sum(w) / Kish n_eff. minres = psych's L-BFGS-B fit (stops early); minres_exact = its exact optimum, the principal-axis fixed point (fm = 'pa', min.err = 1e-14). principal_axis = psych's default iteration (SMC start, min.err 0.001, max.iter 50; converged = fewer than 50 iterations). unrotated = psych's loadings, columns flipped to sum positive; communalities = rowSums(L^2), uniquenesses = 1 - communalities, variance = colSums(L^2). rotations = Pulse's engine definitions put through rot_canon (descending variance diag(Phi L'L), then each column summing positive); psych::fa's own varimax / oblimin asserted equal; psych's 'promax' (kaiser + GPArotation::Varimax) is not stats::promax and is not used. rmsr / empirical_chisq / df / empirical_p = psych fa.stats rms / chi / dof / EPVAL (asserted); objective = sum_{i != j} res^2. kmo = psych::KMO; bartlett = psych::cortest.bartlett(R, n = n_star). The heywood case has no exact optimum (its minres optimum is improper) and is compared on detection only.")
factor_meta$packages <- c(factor_meta$packages, lapply(as.list(pinned_rot), function(v) v))
write_golden("mv_factor", c(factor_meta, list(cases = factor_cases)))
