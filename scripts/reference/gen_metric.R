# gen_metric.R — R oracle for the linalg/metric registry (U27 E1-S1).
#
# Run: Rscript scripts/reference/gen_metric.R
#
# Prints one `name = value` line per oracle case (%.17g). The values are
# pasted into linalg/metric/oracle_test.go (metricOracle); CI never runs
# R. Base R (stats) only.
#
# The pairwise-complete NaN policy is R's stats::dist() convention: an
# element missing on either side is dropped, and an element SUM over the
# p_observed complete pairs is rescaled by p / p_observed (euclidean
# before its square root, manhattan; "maximum" is not rescaled). hamming
# on 0/1 vectors is dist(method = "manhattan") on them, and jaccard is
# 1 - dist(method = "binary"). The remaining metrics are their textbook
# formulas evaluated by base R over the complete pairs.

out <- function(name, v) cat(sprintf("%s = %.17g\n", name, v))
d2 <- function(a, b, method) as.numeric(dist(rbind(a, b), method = method))

# Pairwise-complete rescaling (p = 5, p_observed = 3).
x <- c(1, NA, 3, 4, 7)
y <- c(2, 5, NA, 1, 3)
out("partial_euclidean", d2(x, y, "euclidean"))
out("partial_sq_euclidean", d2(x, y, "euclidean")^2)
out("partial_manhattan", d2(x, y, "manhattan"))
out("partial_chebyshev", d2(x, y, "maximum"))

# Complete vectors: no rescaling.
xc <- c(1.5, 2, -3, 4)
yc <- c(0.5, -1, 2, 2)
out("complete_euclidean", d2(xc, yc, "euclidean"))
out("complete_manhattan", d2(xc, yc, "manhattan"))
out("complete_chebyshev", d2(xc, yc, "maximum"))

# Binary vectors with missing elements (p = 7, p_observed = 5).
u <- c(1, 0, 1, NA, 1, 0, 1)
v <- c(1, 1, 0, 0, NA, 0, 1)
out("binary_hamming", d2(u, v, "manhattan"))
out("binary_jaccard", 1 - d2(u, v, "binary"))

# Correlation and cosine over the complete pairs.
xr <- c(2, 4, NA, 5, 9, 1)
yr <- c(1, 3, 8, NA, 7, 2)
out("partial_correlation", cor(xr, yr, use = "complete.obs"))
ok <- !is.na(xr) & !is.na(yr)
out("partial_cosine", sum(xr[ok] * yr[ok]) / sqrt(sum(xr[ok]^2) * sum(yr[ok]^2)))

# The cosine trap (design doc 07): high cosine, correlation -1.
out("trap_cosine", sum(c(6, 7, 6, 7) * c(7, 6, 7, 6)) / sqrt(sum(c(6, 7, 6, 7)^2) * sum(c(7, 6, 7, 6)^2)))
out("trap_correlation", cor(c(6, 7, 6, 7), c(7, 6, 7, 6)))

# Compositional metrics.
a <- c(0.2, 0.3, 0.5)
b <- c(0.1, 0.6, 0.3)
clr <- function(z) log(z) - mean(log(z))
out("aitchison", sqrt(sum((clr(a) - clr(b))^2)))
h <- c(1, 2, 3, 4)
k <- c(4, 3, 2, 1)
out("hellinger", sqrt(sum((sqrt(h / sum(h)) - sqrt(k / sum(k)))^2) / 2))

# Fitted metrics.
S <- matrix(c(4, 2, 2, 3), 2)
ma <- c(1, 2)
mb <- c(3, 1)
out("mahalanobis", sqrt(mahalanobis(ma, mb, S)))
sdv <- c(2, 0.5, 4)
sa <- c(1, 2, 3)
sb <- c(2, 1, 7)
out("std_euclidean", sqrt(sum(((sa - sb) / sdv)^2)))
mass <- c(0.2, 0.5, 0.3)
ca <- c(10, 20, 30)
cb <- c(5, 30, 5)
out("chi_square", sqrt(sum((ca / sum(ca) - cb / sum(cb))^2 / mass)))
