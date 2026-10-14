# gen_rotation.R — R oracle for Pulse's shared rotation engine
# (U25 E2-S1: varimax, promax and GPA oblimin over a loading matrix; the
# MAT_PCA params.rotation consumer, later MAT_FACTOR and ATTR_PC_SCORE;
# matrix-extensions-hardening E6-S2: the GPA criteria quartimax,
# entropy and geominT / geominQ (Pulse's geomin_orthogonal / geomin),
# each GPArotation's wrapper with its defaults; oblimax is not offered:
# under GPFoblq it collapses factors -- Phi singular, loadings ~1e6 --
# on 6 of these 8 inputs, and its orthogonal form is quartimax).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), numm(),
# strs(), near(), write_golden(), mv_meta(), wvec(), wcov(), sign_fix(),
# listwise() and the fixture data frames (att, mt, lk, wd). Writes
# <out_dir>/mv_rotation.json. CI never runs R: everything is committed.
#
# Pinned packages (refused on any other version, like the parent):
#   GPArotation 2026.8.2 (oblimin, and GPForth's varimax for the
#   orthogonal driver; its default algorithm is the Barzilai-Borwein GPA
#   with a 10-iterate non-monotone line search, eps 1e-5, maxit 2000)
# Install: Rscript -e 'install.packages("GPArotation",
#   lib = Sys.getenv("R_LIBS_USER"), repos = "https://cloud.r-project.org")'
#
# Every case's input A is the first k columns of the PCA loadings of the
# fixture's correlation (eigen(R) with Pulse's eigenvector sign rule,
# loadings = v * sqrt(lambda) — exactly mv_pca.json's construction), so
# the same A is what MAT_PCA rotates end to end.
#
# The raw R rotations are then put through Pulse's ONE sign-and-order
# rule (psych::fa's): columns ordered by descending variance accounted,
# diag(Phi %*% t(L) %*% L) (the sum of squared loadings when orthogonal;
# a tie keeps the earlier column), then each column flipped so it sums
# positive (a zero sum stays). The rotation matrix and Phi follow the
# same permutation and signs: L = A %*% rotation, Phi = solve(rotation)
# %*% t(solve(rotation)) (the identity for varimax).

pinned_rot <- c(GPArotation = "2026.8.2")
for (p in names(pinned_rot)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != pinned_rot[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, pinned_rot[[p]]))
  }
}

rot_canon <- function(L, rotmat, Phi) {
  L <- unname(as.matrix(L)); rotmat <- unname(as.matrix(rotmat)); Phi <- unname(as.matrix(Phi))
  v <- diag(Phi %*% crossprod(L))
  o <- order(-v) # stable: a tie keeps the earlier column
  L <- L[, o, drop = FALSE]; rotmat <- rotmat[, o, drop = FALSE]; Phi <- Phi[o, o, drop = FALSE]; v <- v[o]
  s <- sign(colSums(L)); s[s == 0] <- 1
  D <- diag(s, length(s))
  list(loadings = L %*% D, rotation = rotmat %*% D, phi = D %*% Phi %*% D, variance = v, order = o - 1, signs = s)
}

rot_cases <- list()
add_rot <- function(fx, df, cols, kind, k) {
  d <- listwise(df, cols)
  X <- as.matrix(d[, cols])
  w <- wvec(d, kind)
  R <- cov2cor(wcov(X, w, kind))
  e <- eigen(R, symmetric = TRUE)
  V <- sign_fix(e$vectors)
  A <- sweep(V, 2, sqrt(pmax(e$values, 0)), "*")[, seq_len(k), drop = FALSE]

  vm <- stats::varimax(A)
  near(A %*% vm$rotmat, vm$loadings, 1e-12, paste("varimax rotmat", fx))
  out_vm <- rot_canon(vm$loadings, vm$rotmat, diag(k))

  promax_one <- function(kappa) {
    pm <- stats::promax(A, m = kappa)
    near(A %*% pm$rotmat, pm$loadings, 1e-12, paste("promax rotmat", fx))
    ui <- solve(pm$rotmat)
    c(rot_canon(pm$loadings, pm$rotmat, ui %*% t(ui)), list(kappa = num(kappa)))
  }
  oblimin_one <- function(gamma) {
    ob <- GPArotation::oblimin(A, gam = gamma)
    rotmat <- t(solve(ob$Th))
    near(A %*% rotmat, unclass(ob$loadings), 1e-12, paste("oblimin rotmat", fx))
    near(solve(rotmat) %*% t(solve(rotmat)), ob$Phi, 1e-12, paste("oblimin phi", fx))
    if (!ob$convergence) stop("oblimin did not converge on ", fx)
    c(rot_canon(ob$loadings, rotmat, ob$Phi),
      list(gamma = num(gamma), iterations = num(nrow(ob$Table) - 1)))
  }
  # The orthogonal GPA driver (E6-S1): GPForth on vgQ.varimax with
  # Kaiser normalization -- a DIFFERENT algorithm from stats::varimax
  # (they agree only to ~1e-3: different stopping rules), so it pins the
  # driver step for step rather than the production varimax.
  gpforth_varimax <- function() {
    gf <- GPArotation::GPForth(A, method = "varimax", normalize = TRUE)
    near(A %*% gf$Th, unclass(gf$loadings), 1e-12, paste("gpforth rotmat", fx))
    if (!gf$convergence) stop("GPForth varimax did not converge on ", fx)
    c(rot_canon(gf$loadings, gf$Th, diag(k)), list(iterations = num(nrow(gf$Table) - 1)))
  }
  # E6-S2: GPArotation's wrappers (normalize = FALSE, algorithm 'bb',
  # eps 1e-5, maxit 2000, randomStarts = 0 -- one start at the identity,
  # the start Pulse's driver uses). Orthogonal results carry Th as the
  # rotation and Phi = I; oblique ones t(solve(Th)) and Phi = t(Th) Th.
  #
  # sensitivity: how far R's OWN result moves when A moves by a relative
  # 1e-12 (a fixed +-1 pattern) -- the max absolute change of the
  # canonicalised loadings, or 1 when the column order or signs change.
  # A long Barzilai-Borwein trajectory can amplify a rounding-sized input
  # change into a different stopping iterate inside the 1e-5 gradient
  # band (geomin on two inputs here); such a run cannot be compared at
  # rotationOracleTol by anything that rounds differently from LAPACK,
  # and the Go test widens it to the GPA band only where this says so.
  gpa_one <- function(fn, label, extra = list()) {
    r <- fn(A)
    if (!r$convergence) stop(label, " did not converge on ", fx)
    pert <- A * (1 + 1e-12 * outer(seq_len(nrow(A)), seq_len(ncol(A)), function(i, j) (-1)^(i + j)))
    r2 <- fn(pert)
    if (r$orthogonal) {
      rotmat <- r$Th; phi <- diag(k)
    } else {
      rotmat <- t(solve(r$Th)); phi <- r$Phi
      near(solve(rotmat) %*% t(solve(rotmat)), phi, 1e-12, paste(label, "phi", fx))
    }
    near(A %*% rotmat, unclass(r$loadings), 1e-12, paste(label, "rotmat", fx))
    out <- rot_canon(r$loadings, rotmat, phi)
    out2 <- rot_canon(r2$loadings, if (r2$orthogonal) r2$Th else t(solve(r2$Th)), if (r2$orthogonal) diag(k) else r2$Phi)
    sens <- if (identical(out$order, out2$order) && identical(out$signs, out2$signs)) max(abs(out$loadings - out2$loadings)) else 1
    c(out, extra, list(iterations = num(nrow(r$Table) - 1), sensitivity = num(sens)))
  }
  enc <- function(r) {
    out <- list(loadings = numm(r$loadings), rotation = numm(r$rotation), phi = numm(r$phi),
                variance = numv(r$variance), order = r$order, signs = r$signs)
    for (key in c("kappa", "gamma", "epsilon", "iterations", "sensitivity")) if (!is.null(r[[key]])) out[[key]] <- r[[key]]
    out
  }
  rot_cases[[length(rot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), weight = kind, k = num(k),
    unrotated = numm(A),
    varimax = enc(out_vm),
    promax = lapply(c(4, 2), function(kp) enc(promax_one(kp))),
    oblimin = lapply(c(0, 0.5), function(g) enc(oblimin_one(g))),
    gpforth_varimax = enc(gpforth_varimax()),
    quartimax = enc(gpa_one(function(x) GPArotation::quartimax(x), "quartimax")),
    entropy = enc(gpa_one(function(x) GPArotation::entropy(x), "entropy")),
    geomin = lapply(c(0.01, 0.1), function(e) enc(gpa_one(function(x) GPArotation::geominQ(x, delta = e), "geominQ", list(epsilon = num(e))))),
    geomin_orthogonal = lapply(c(0.01, 0.1), function(e) enc(gpa_one(function(x) GPArotation::geominT(x, delta = e), "geominT", list(epsilon = num(e)))))
  )
}
add_rot("attitude", att, names(datasets::attitude), "none", 2)
add_rot("attitude", att, names(datasets::attitude), "none", 3)
add_rot("attitude", att, names(datasets::attitude), "probability", 3)
add_rot("mtcars", mt, names(datasets::mtcars), "none", 2)
add_rot("mtcars", mt, names(datasets::mtcars), "none", 3)
add_rot("mtcars", mt, names(datasets::mtcars), "frequency", 3)
add_rot("likert_ties", lk, paste0("q", 1:6), "none", 2)
add_rot("weighted", wd, paste0("x", 1:4), "frequency", 2)

rot_meta <- mv_meta("factor rotation (varimax, promax, oblimin, GPForth varimax, quartimax, entropy, geomin, geomin_orthogonal)",
  "stats::varimax(A) (Kaiser normalization, eps 1e-5); stats::promax(A, m = kappa); GPArotation::oblimin(A, gam = gamma) (normalize = FALSE, algorithm 'bb', eps 1e-5, maxit 2000); GPArotation::GPForth(A, method = 'varimax', normalize = TRUE) (algorithm 'bb', eps 1e-5, maxit 2000); GPArotation::quartimax(A), entropy(A), geominQ(A, delta = epsilon) (Pulse geomin), geominT(A, delta = epsilon) (Pulse geomin_orthogonal) (normalize = FALSE, randomStarts = 0, algorithm 'bb', eps 1e-5, maxit 2000)",
  "A = the first k PCA loadings of the fixture's listwise correlation (mv_pca.json's construction: eigenvector sign rule, v * sqrt(lambda)). Every rotation is put through Pulse's sign-and-order rule: columns ordered by descending variance accounted diag(Phi t(L) L) (ties keep the earlier column), then each flipped so it sums positive; rotation and phi follow. L = A %*% rotation; phi = solve(rotation) %*% t(solve(rotation)) (identity for varimax; for oblimin t(Th) %*% Th, asserted). order is the 0-based source column of each output column; iterations is the GPA iteration count (nrow(Table) - 1) of every GPA criterion. geomin / geomin_orthogonal list epsilon 0.01 (the default) and 0.1. sensitivity (those four criteria) is the max absolute change of R's own canonicalised loadings when A is scaled cellwise by 1 +- 1e-12 (1 when the order or signs change): above 1e-9 the run's trajectory amplifies rounding, and it is compared within the GPA band rather than to the last digit.")
rot_meta$packages <- c(rot_meta$packages, lapply(as.list(pinned_rot), function(v) v))
write_golden("mv_rotation", c(rot_meta, list(cases = rot_cases)))
