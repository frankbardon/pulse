# gen_singularity.R — R oracle for Pulse's ONE singular decision across
# the multivariate stack (TODO #408, internal/processing/singular_rule.go).
#
# Sourced by gen_multivariate.R (`make reference`) after gen_distance.R:
# it reuses out_dir, num(), numv(), numm(), strs(), write_golden() and
# mv_meta(). Writes <out_dir>/mv_singularity.json. CI never runs R:
# everything here is committed. Matrix (already pinned) + base R — no
# new package pin.
#
# The rule: a covariance-shaped S is singular when the smallest
# eigenvalue of its correlation form R is at or below the one rank
# tolerance p * .Machine$double.eps * max(eigen(R)) — Matrix::rankMatrix's
# default "tolNorm2" tolerance (max(dim) * eps relative to the largest
# singular value), so `singular` is asserted equal to
# rankMatrix(R) < p. chol_ok records whether base::chol(R) still
# factors it: TRUE on `near_singular` is the borderline the old Pulse
# rule ("singular only when the reference Cholesky fails") called
# invertible.
#
# Every case carries its own rows (seeded, written into the golden so no
# shared fixture moves): four members x1..x4 and a two-level group g
# (alternating 1, 2) for the split tests.
#   * near_singular  x4 = x1 + 4e-8 * z: the correlation's smallest
#                    eigenvalue sits a few ulps above 0, well inside the
#                    tolerance, while chol() still succeeds;
#   * full_rank      the same draw with x4 = x1 + 0.5 * z (control:
#                    every surface keeps its figures);
#   * exact_dependency x4 = x1 + x2 + x3 in double precision.

sing_cases <- list()

sing_case <- function(name, delta, exact = FALSE) {
  set.seed(911)
  n <- 40
  X <- matrix(rnorm(n * 3), n)
  z <- rnorm(n)
  x4 <- if (exact) X[, 1] + X[, 2] + X[, 3] else X[, 1] + delta * z
  M <- cbind(X, x4)
  colnames(M) <- paste0("x", 1:4)
  p <- ncol(M)
  R <- cor(M)
  ev <- eigen(R, symmetric = TRUE, only.values = TRUE)$values
  tol <- p * .Machine$double.eps * max(ev)
  singular <- min(ev) <= tol
  rk <- as.integer(Matrix::rankMatrix(R))
  if (singular != (rk < p)) stop(name, ": the tolerance rule and rankMatrix disagree")
  chol_ok <- !inherits(try(chol(R), silent = TRUE), "try-error")
  g <- rep(c(1, 2), length.out = n)
  sing_cases[[length(sing_cases) + 1]] <<- list(
    name = name, delta = if (exact) jnull else num(delta),
    members = strs(colnames(M)), rows = numm(M), groups = numv(g),
    correlation_eigenvalues = numv(ev), tolerance = num(tol),
    singular = singular, rank = num(rk), chol_ok = chol_ok
  )
}

sing_case("near_singular", 4e-8)
sing_case("full_rank", 0.5)
sing_case("exact_dependency", NA, exact = TRUE)
if (!sing_cases[[1]]$singular || !sing_cases[[1]]$chol_ok) stop("near_singular is not the borderline (singular under the tolerance, chol() succeeding)")
if (sing_cases[[2]]$singular) stop("full_rank is singular")

sing_doc <- c(mv_meta("singular decision (the one rank-tolerance rule)",
  "eigen(cor(X), symmetric = TRUE), Matrix::rankMatrix(cor(X)), base::chol(cor(X))",
  "singular = min eigenvalue of the correlation <= p * eps * max eigenvalue (== rankMatrix < p, asserted); chol_ok = chol() succeeds. near_singular is singular with chol_ok TRUE: the borderline every Pulse surface must now call singular."),
  list(cases = sing_cases))
write_golden("mv_singularity", sing_doc)
