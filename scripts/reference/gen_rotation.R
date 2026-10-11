# gen_rotation.R — R oracle for Pulse's shared rotation engine
# (U25 E2-S1: varimax, promax and GPA oblimin over a loading matrix; the
# MAT_PCA params.rotation consumer, later MAT_FACTOR and ATTR_PC_SCORE).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# weight utilities exist: it reuses out_dir, num(), numv(), numm(),
# strs(), near(), write_golden(), mv_meta(), wvec(), wcov(), sign_fix(),
# listwise() and the fixture data frames (att, mt, lk, wd). Writes
# <out_dir>/mv_rotation.json. CI never runs R: everything is committed.
#
# Pinned packages (refused on any other version, like the parent):
#   GPArotation 2026.8.2 (oblimin; its default algorithm is the
#   Barzilai-Borwein GPA with a 10-iterate non-monotone line search,
#   eps 1e-5, maxit 2000)
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
  enc <- function(r) {
    out <- list(loadings = numm(r$loadings), rotation = numm(r$rotation), phi = numm(r$phi),
                variance = numv(r$variance), order = r$order, signs = r$signs)
    for (key in c("kappa", "gamma", "iterations")) if (!is.null(r[[key]])) out[[key]] <- r[[key]]
    out
  }
  rot_cases[[length(rot_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), weight = kind, k = num(k),
    unrotated = numm(A),
    varimax = enc(out_vm),
    promax = lapply(c(4, 2), function(kp) enc(promax_one(kp))),
    oblimin = lapply(c(0, 0.5), function(g) enc(oblimin_one(g)))
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

rot_meta <- mv_meta("factor rotation (varimax, promax, oblimin)",
  "stats::varimax(A) (Kaiser normalization, eps 1e-5); stats::promax(A, m = kappa); GPArotation::oblimin(A, gam = gamma) (normalize = FALSE, algorithm 'bb', eps 1e-5, maxit 2000)",
  "A = the first k PCA loadings of the fixture's listwise correlation (mv_pca.json's construction: eigenvector sign rule, v * sqrt(lambda)). Every rotation is put through Pulse's sign-and-order rule: columns ordered by descending variance accounted diag(Phi t(L) L) (ties keep the earlier column), then each flipped so it sums positive; rotation and phi follow. L = A %*% rotation; phi = solve(rotation) %*% t(solve(rotation)) (identity for varimax; for oblimin t(Th) %*% Th, asserted). order is the 0-based source column of each output column; iterations is oblimin's GPA iteration count (nrow(Table) - 1).")
rot_meta$packages <- c(rot_meta$packages, lapply(as.list(pinned_rot), function(v) v))
write_golden("mv_rotation", c(rot_meta, list(cases = rot_cases)))
