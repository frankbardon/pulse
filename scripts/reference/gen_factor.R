# gen_factor.R — R oracle for Pulse's MAT_FACTOR (U25 E2-S2: exploratory
# factor analysis by minres and principal-axis extraction, then the
# shared rotation engine; E2-S3: maximum-likelihood extraction and its
# fit indices).
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
#   * ml: psych::fa(R, k, fm = "ml", rotate = "none", n.obs = n_star)
#     (L-BFGS-B over psi in [0.005, 1] from 1 - SMC, default factr —
#     stops early), psi = 1 - its communalities; ml_exact: the optimum
#     itself (fa_ml_exact: L-BFGS-B at factr = pgtol = 0, then Newton on
#     the free psi with stats::optimHess to a free gradient < 1e-13),
#     stats::factanal asserted within 1e-6 where it can start. Each
#     entry's likelihood figures are psych::fa.stats(r = R, f = L,
#     n.obs = n_star, alpha = 0.1) on its loadings: STATISTIC, PVAL,
#     TLI, RMSEA[1], BIC (null at dof <= 0), and the 90% RMSEA interval
#     (TODO #405, psych::fa's default alpha = 0.1): fa_rmsea_ci below,
#     psych's own rule solved to machine precision, asserted within
#     psych's uniroot tolerance of fa.stats' RMSEA lower / upper.

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
  # E6-S2: the further GPA criteria, GPArotation's wrappers at their
  # defaults (geomin delta = 0.01, Pulse's params.epsilon default).
  gpa <- function(r, label) {
    if (!r$convergence) stop(label, " did not converge")
    if (r$orthogonal) rot_canon(r$loadings, r$Th, diag(k)) else rot_canon(r$loadings, t(solve(r$Th)), r$Phi)
  }
  list(
    varimax = rot_canon(vm$loadings, vm$rotmat, diag(k)),
    promax = rot_canon(pm$loadings, pm$rotmat, ui %*% t(ui)),
    oblimin = rot_canon(ob$loadings, orot, ob$Phi),
    quartimax = gpa(GPArotation::quartimax(A), "quartimax"),
    entropy = gpa(GPArotation::entropy(A), "entropy"),
    geomin = gpa(GPArotation::geominQ(A, delta = 0.01), "geominQ"),
    geomin_orthogonal = gpa(GPArotation::geominT(A, delta = 0.01), "geominT")
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


# ---- maximum likelihood (U25 E2-S3)
# psych::fa(fm = "ml")'s objective and gradient (psych's internal FAfn /
# FAgr, = stats::factanal's factanal.fit.mle): F(psi) = sum_{m>k}(e_m -
# log e_m) - (p - k), e the eigenvalues of Psi^-1/2 R Psi^-1/2.
fa_ml_fn <- function(Psi, S, nf) {
  sc <- diag(1 / sqrt(Psi)); E <- eigen(sc %*% S %*% sc, symmetric = TRUE, only.values = TRUE)
  e <- E$values[-(1:nf)]
  -(sum(log(e) - e) - nf + nrow(S))
}
fa_ml_gr <- function(Psi, S, nf) {
  sc <- diag(1 / sqrt(Psi)); E <- eigen(sc %*% S %*% sc, symmetric = TRUE)
  load <- E$vectors[, 1:nf, drop = FALSE] %*% diag(sqrt(pmax(E$values[1:nf] - 1, 0)), nf)
  load <- diag(sqrt(Psi)) %*% load
  g <- load %*% t(load) + diag(Psi) - S
  diag(g) / Psi^2
}
# fa_ml_loadings is psych's FAout plus psych::fa's sign (columns summing
# positive) and order (descending colSums(L^2), k > 1) rule.
fa_ml_loadings <- function(Psi, S, nf) {
  sc <- diag(1 / sqrt(Psi)); E <- eigen(sc %*% S %*% sc, symmetric = TRUE)
  L <- diag(sqrt(Psi)) %*% E$vectors[, 1:nf, drop = FALSE] %*% diag(sqrt(pmax(E$values[1:nf] - 1, 0)), nf)
  s <- sign(colSums(L)); s[s == 0] <- 1
  L <- L %*% diag(s, nf)
  if (nf > 1) L <- L[, order(colSums(L^2), decreasing = TRUE), drop = FALSE]
  L
}
# fa_ml_exact is the optimum on the box [0.005, 1] (psych's bounds):
# L-BFGS-B from psych's start (1 - SMC) at factr = pgtol = 0, then Newton
# steps on the free psi with stats::optimHess's finite-difference Hessian
# of the analytic gradient until the free gradient is below 1e-13 — an
# independent route to the point Pulse's exact-Hessian solver reaches.
fa_ml_exact <- function(R, k) {
  p <- ncol(R)
  o <- optim(pmin(pmax(1 - psych::smc(R), 0.005), 1), fa_ml_fn, fa_ml_gr, method = "L-BFGS-B", lower = 0.005, upper = 1,
             control = list(parscale = rep(0.01, p), factr = 0, pgtol = 0, maxit = 1e5), S = R, nf = k)
  psi <- o$par
  for (it in 1:100) {
    g <- fa_ml_gr(psi, R, k)
    free <- !((psi <= 0.005 & g > 0) | (psi >= 1 & g < 0))
    if (max(abs(g[free])) < 1e-13) break
    H <- optimHess(psi, fa_ml_fn, fa_ml_gr, S = R, nf = k, control = list(ndeps = rep(1e-6, p)))
    psi[free] <- pmin(pmax(psi[free] - solve(H[free, free, drop = FALSE], g[free]), 0.005), 1)
  }
  g <- fa_ml_gr(psi, R, k)
  free <- !((psi <= 0.005 & g > 0) | (psi >= 1 & g < 0))
  if (max(abs(g[free])) >= 1e-10) stop("exact ML did not converge: ", max(abs(g[free])))
  psi
}
# fa_rmsea_ci is psych::fa.stats' RMSEA interval transcribed: tail =
# (1 - conf)/2; the upper bound solves pchisq(chi, df, ncp = x) = tail
# when pchisq(chi, df) > tail (else 0), the lower one = 1 - tail when
# pchisq(chi, df) > 1 - tail (else 0), each on [0, max(N, chi) + 2N] and
# rescaled sqrt(x / (N - 1) / df); no root in the bracket is NA (an
# upper bound whose point RMSEA is 0 is 0), and a bound on the wrong side
# of the point estimate is NA. psych runs uniroot at its default tol
# (~1.2e-4 on x); this solves to the last bit (tol 1e-300), so its
# bounds are the exact ones psych's approximate.
fa_rmsea_ci <- function(chi, df, N, conf = 0.9) {
  tail <- (1 - conf) / 2
  rmsea <- sqrt(max(chi / (df * N) - 1 / (N - 1), 0))
  mx <- max(N, chi) + 2 * N
  f0 <- pchisq(chi, df)
  root <- function(target) {
    f <- function(x) pchisq(chi, df, ncp = x) - target
    if (f(mx) > 0) return(NULL)
    uniroot(f, c(0, mx), tol = 1e-300, maxiter = 10000)$root
  }
  up <- 0
  if (f0 > tail) {
    r <- root(tail)
    up <- if (is.null(r)) (if (rmsea <= 0) 0 else NA_real_) else sqrt(r / (N - 1) / df)
  }
  lo <- 0
  if (f0 > 1 - tail) {
    r <- root(1 - tail)
    lo <- if (is.null(r)) NA_real_ else sqrt(r / (N - 1) / df)
  }
  if (!is.na(up) && up < rmsea) up <- NA_real_
  if (!is.na(lo) && lo > rmsea) lo <- NA_real_
  c(lower = lo, upper = up)
}

# fa_ml_entry records an ML solution: the loadings / communalities /
# uniquenesses / variance, psi, the empirical residual figures (fa_stats)
# and the likelihood figures of psych::fa.stats on these loadings
# (STATISTIC, dof, PVAL, TLI, RMSEA[1], BIC; null at dof <= 0) plus the
# 90% RMSEA interval (fa_rmsea_ci, checked against fa.stats' own).
fa_ml_entry <- function(R, L, psi, ns, k) {
  st <- fa_stats(R, L, ns)
  fs <- suppressWarnings(suppressMessages(psych::fa.stats(r = R, f = L, n.obs = ns, alpha = 0.1)))
  near(fs$dof, st$df, 0, "ml dof")
  pos <- st$df > 0
  ci <- c(lower = NA_real_, upper = NA_real_)
  if (pos) {
    ci <- fa_rmsea_ci(fs$STATISTIC, fs$dof, ns)
    near(fs$RMSEA[["confidence"]], 0.9, 0, "ml RMSEA confidence")
    # psych's uniroot default tol (.Machine$double.eps^0.25 on the
    # noncentrality) bounds its distance from the exact root.
    near(ci, fs$RMSEA[c("lower", "upper")], 1e-5, "ml RMSEA interval vs psych")
  }
  h <- rowSums(L^2)
  e <- list(unrotated = numm(L), communalities = numv(h), uniquenesses = numv(1 - h), variance = numv(colSums(L^2)),
            psi = numv(psi),
            rmsr = num(st$rmsr), empirical_chisq = num(st$chisq), df = num(st$df), empirical_p = numx(st$p),
            objective = num(fs$objective), likelihood_chisq = num(fs$STATISTIC),
            likelihood_p = numx(if (pos) fs$PVAL else NA_real_), tli = numx(if (pos) fs$TLI else NA_real_),
            rmsea = numx(if (pos) fs$RMSEA[[1]] else NA_real_), bic = numx(if (pos) fs$BIC else NA_real_),
            rmsea_lower = numx(ci[["lower"]]), rmsea_upper = numx(ci[["upper"]]))
  rot <- fa_rotations(L, k)
  if (!is.null(rot)) e$rotations <- fa_enc_rot(rot)
  e
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

  # Maximum likelihood: psych's own fit (L-BFGS-B, default factr; psi =
  # 1 - communalities, psych's result.res$par) and the exact optimum.
  ml <- fa_quiet(r = R, nfactors = k, fm = "ml", rotate = "none", n.obs = ns)
  ml_psi <- 1 - ml$communalities
  near(fa_ml_loadings(ml_psi, R, k), fa_flip(ml), 1e-10, paste("ml loadings rule", fx))
  e <- fa_ml_entry(R, fa_flip(ml), ml_psi, ns, k)
  near(fa_stats(R, fa_flip(ml), ns)$chisq, ml$chi, 1e-10, paste("ml chi", fx))
  near(as.numeric(e$likelihood_chisq), ml$STATISTIC, 1e-10, paste("ml STATISTIC", fx))
  out$ml <- e
  ex_psi <- fa_ml_exact(R, k)
  ex_L <- fa_ml_loadings(ex_psi, R, k)
  out$ml_exact <- fa_ml_entry(R, ex_L, ex_psi, ns, k)
  # stats::factanal (its own start, (1 - k/(2p)) / diag(R^-1)) reaches the
  # same optimum at a tight factr where it can start at all.
  fl <- tryCatch(factanal(covmat = R, factors = k, n.obs = ns,
                          control = list(opt = list(factr = 1e-3, pgtol = 0, maxit = 1e5))), error = function(err) NULL)
  if (!is.null(fl)) near(fl$uniquenesses, ex_psi, 1e-6, paste("factanal uniquenesses", fx))
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

factor_meta <- mv_meta("exploratory factor analysis (minres, principal axis, maximum likelihood) with rotation",
  "psych::fa(r = R, nfactors = k, fm = 'minres' | 'pa' | 'ml', rotate = 'none', n.obs = n_star); psych::fa.stats(r = R, f = L, n.obs = n_star); stats::varimax, stats::promax(m = 4), GPArotation::oblimin(gam = 0), quartimax, entropy, geominQ / geominT(delta = 0.01) (normalize = FALSE) on the unrotated loadings",
  "R = the fixture's listwise (weighted) correlation; n_star = N / sum(w) / Kish n_eff. minres = psych's L-BFGS-B fit (stops early); minres_exact = its exact optimum, the principal-axis fixed point (fm = 'pa', min.err = 1e-14). principal_axis = psych's default iteration (SMC start, min.err 0.001, max.iter 50; converged = fewer than 50 iterations). unrotated = psych's loadings, columns flipped to sum positive; communalities = rowSums(L^2), uniquenesses = 1 - communalities, variance = colSums(L^2). rotations = Pulse's engine definitions put through rot_canon (descending variance diag(Phi L'L), then each column summing positive); psych::fa's own varimax / oblimin asserted equal; psych's 'promax' (kaiser + GPArotation::Varimax) is not stats::promax and is not used. rmsr / empirical_chisq / df / empirical_p = psych fa.stats rms / chi / dof / EPVAL (asserted); objective = sum_{i != j} res^2. kmo = psych::KMO; bartlett = psych::cortest.bartlett(R, n = n_star). The heywood case has no exact optimum (its minres optimum is improper) and is compared on detection only. ml = psych::fa(fm = 'ml') (L-BFGS-B over psi in [0.005, 1] from 1 - SMC, default factr: stops early); psi = 1 - its communalities (psych's optimiser output); its loadings asserted equal to psych's FAout + sign + descending-variance order rule. ml_exact = the optimum itself: L-BFGS-B at factr = pgtol = 0, then Newton on the free psi with optimHess until the free gradient is below 1e-13; stats::factanal at factr 1e-3 asserted within 1e-6 where it starts. Each ML entry's likelihood figures are psych::fa.stats on its loadings: objective = tr(M^-1 R) - log det(M^-1 R) - p (M = LL' with unit diagonal), likelihood_chisq = STATISTIC (objective * ((N - 1) - (2p + 5)/6 - 2k/3), 0 when negative), likelihood_p = PVAL, tli = TLI, rmsea = RMSEA[1] (point estimate), bic = BIC; the last four null at df <= 0. rmsea_lower / rmsea_upper = the 90% RMSEA interval (fa.stats alpha = 0.1, psych::fa's default): psych's rule (tail 0.05; upper solves pchisq(chi, df, ncp) = 0.05 when pchisq(chi, df) > 0.05, else 0; lower = 0.95 likewise; root on [0, max(N, chi) + 2N], sqrt(ncp / (N - 1) / df); out of bracket or on the wrong side of the point estimate = null) solved by uniroot at tol 1e-300 (psych's default tol leaves its own bounds ~1e-7 away; asserted within 1e-5); null at df <= 0.")
factor_meta$packages <- c(factor_meta$packages, lapply(as.list(pinned_rot), function(v) v))
write_golden("mv_factor", c(factor_meta, list(cases = factor_cases)))
