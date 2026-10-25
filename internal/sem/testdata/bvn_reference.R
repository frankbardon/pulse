# The bivariate-normal CDF reference table (bvn_reference.json) the
# BivariateNormalCDF port is held to (TestPolychoric_BVNMatchesReference,
# 1e-12 absolute). Every point carries two independent references:
#   pbivnorm  — R's pbivnorm (Genz's MVBVU, the routine lavaan's
#               polychoric correlations call: the algorithm ported);
#   sheppard  — an adaptive quadrature (integrate, rel.tol 2e-14, abs.tol 1e-17) of
#               Sheppard's arcsine integral
#                 Φ₂(h, k; ρ) = Φ(h)Φ(k) + 1/(2π) ∫₀^asin ρ
#                   exp(−(h² + k² − 2hk sin θ) / (2 cos² θ)) dθ,
#               no Genz code at all; exact at ρ = ±1
#               (Φ(min(h, k)), max(0, Φ(h) + Φ(k) − 1)).
# The grid spans the tails (|h|, |k| up to 8), ρ = ±1 and the branch
# edges of the port (|ρ| at 0.3, 0.75 and 0.925, and just inside).
#   Rscript bvn_reference.R > bvn_reference.json
suppressPackageStartupMessages({ library(pbivnorm); library(jsonlite) })
sheppard <- function(h, k, r) {
  if (r == 1) return(pnorm(min(h, k)))
  if (r == -1) return(max(0, pnorm(h) + pnorm(k) - 1))
  if (r == 0) return(pnorm(h) * pnorm(k))
  f <- function(t) exp(-(h^2 + k^2 - 2 * h * k * sin(t)) / (2 * cos(t)^2))
  pnorm(h) * pnorm(k) +
    integrate(f, 0, asin(r), rel.tol = 2e-14, abs.tol = 1e-17, subdivisions = 5000L)$value / (2 * pi)
}
hs <- c(-8, -4.5, -2, -1, -0.25, 0, 0.5, 1.5, 3, 6)
ks <- c(-7, -3, -1.2, 0, 0.4, 2.5, 5)
rhos <- c(-1, -0.99999, -0.9999, -0.999, -0.99, -0.95, -0.93, -0.925, -0.9249, -0.9,
          -0.75, -0.7499, -0.5, -0.3, -0.2999, -0.1, 0, 0.1, 0.2999, 0.3, 0.5,
          0.7499, 0.75, 0.9, 0.9249, 0.925, 0.95, 0.99, 0.999, 0.9999, 0.99999, 1)
pts <- list(); worst <- 0
for (r in rhos) for (h in hs) for (k in ks) {
  a <- pbivnorm(h, k, rho = r)
  b <- sheppard(h, k, r)
  worst <- max(worst, abs(a - b))
  pts[[length(pts) + 1]] <- list(h = h, k = k, rho = r, pbivnorm = a, sheppard = b)
}
out <- list(
  pbivnorm = as.character(packageVersion("pbivnorm")),
  r = R.version.string,
  max_abs_reference_gap = worst,
  points = pts)
cat(toJSON(out, auto_unbox = TRUE, digits = I(17), pretty = FALSE), "\n")
