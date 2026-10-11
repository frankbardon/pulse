# gen_distance.R — R oracle for Pulse's MAT_DISTANCE (U25 E4-S2).
#
# Sourced by gen_multivariate.R (`make reference`) after gen_kmeans.R:
# it reuses out_dir, num(), numv(), numm(), near(), write_golden(),
# mv_meta() and gen_kmeans.R's km_cases. Writes
# <out_dir>/mv_distance.json. CI never runs R: everything here is
# committed. Base R (stats) only — no new package pin.
#
# MAT_DISTANCE compares the centroids of the buckets a grouper cuts the
# rows into. Every case carries its own rows (seeded, written into the
# golden so no shared fixture moves), each row's bucket number (Pulse's
# emission order: 1-based) and, when weighted, its weights. Per case:
#   * the centroids — per bucket weighted.mean() of every member;
#   * the bucket masses (sum of the weights) and row counts;
#   * standardize: the centroids z-scored by the bucketed rows' mean
#     and sd on w* = w * N* / sum(w) (N* = sum(w) frequency, Kish n_eff
#     probability; unweighted: scale()), asserted against scale() and,
#     under frequency weights, against the rep()-expanded rows;
#   * per metric the k x k matrix over those points: stats::dist()
#     "euclidean" / "manhattan" / "maximum" (chebyshev), the squared
#     euclidean, cosine 1 - a.b / (|a| |b|), and mahalanobis
#     sqrt(stats::mahalanobis(c_a, c_b, S)) on the RAW centroids with S
#     the pooled within-bucket covariance on w*,
#     sum_b sum_i w*_i (x_i - c_b)(x_i - c_b)' / (sum(w*) - k) —
#     unweighted, asserted equal to the lm(X ~ bucket) residual
#     covariance.
#   * kmeans_blobs3: gen_kmeans.R's blobs3 rows partitioned by R's
#     cluster numbers (the partition every k-means start reaches), so a
#     GROUP_KMEANS between reproduces it.

set.seed(903)
dist_cases <- list()

dist_case <- function(name, X, g, k, kind, standardize, w = NULL) {
  X <- as.matrix(X)
  colnames(X) <- paste0("x", seq_len(ncol(X)))
  p <- ncol(X)
  wv <- if (is.null(w)) rep(1, nrow(X)) else w
  n_star <- if (kind == "probability") sum(wv)^2 / sum(wv^2) else sum(wv)
  ws <- wv * n_star / sum(wv)
  cent <- t(sapply(seq_len(k), function(b) {
    sel <- g == b
    sapply(seq_len(p), function(j) weighted.mean(X[sel, j], wv[sel]))
  }))
  if (p == 1) cent <- t(cent)
  sizes <- sapply(seq_len(k), function(b) sum(wv[g == b]))
  counts <- sapply(seq_len(k), function(b) sum(g == b))
  mu <- colSums(ws * X) / sum(ws)
  sdv <- sqrt(colSums(ws * sweep(X, 2, mu)^2) / (sum(ws) - 1))
  if (kind == "none") {
    near(mu, attr(scale(X), "scaled:center"), 1e-12, paste(name, "center"))
    near(sdv, attr(scale(X), "scaled:scale"), 1e-12, paste(name, "scale"))
  }
  if (kind == "frequency") {
    E <- X[rep(seq_len(nrow(X)), wv), , drop = FALSE]
    gE <- g[rep(seq_len(nrow(X)), wv)]
    near(sdv, apply(E, 2, sd), 1e-12, paste(name, "expanded sd"))
    near(cent, t(sapply(seq_len(k), function(b) colMeans(E[gE == b, , drop = FALSE]))), 1e-12, paste(name, "expanded centroids"))
  }
  Z <- sweep(sweep(cent, 2, mu), 2, sdv, "/")
  P <- if (standardize) Z else cent
  D <- function(method) as.matrix(stats::dist(P, method = method))
  nrm <- sqrt(rowSums(P^2))
  cosd <- 1 - (P %*% t(P)) / outer(nrm, nrm)
  diag(cosd) <- 0
  S <- matrix(0, p, p)
  for (b in seq_len(k)) {
    sel <- g == b
    R <- sweep(X[sel, , drop = FALSE], 2, cent[b, ])
    S <- S + t(R) %*% (ws[sel] * R)
  }
  S <- S / (sum(ws) - k)
  if (kind == "none") {
    fit <- lm(X ~ factor(g))
    near(S, crossprod(resid(fit)) / (nrow(X) - k), 1e-10, paste(name, "pooled vs lm residual"))
  }
  maha <- sapply(seq_len(k), function(b) sqrt(stats::mahalanobis(cent, cent[b, ], S)))
  dist_cases[[length(dist_cases) + 1]] <<- list(
    name = name, k = num(k), kind = kind, standardize = standardize,
    rows = numm(X), buckets = numv(g), weights = if (is.null(w)) jnull else numv(w),
    sizes = numv(sizes), counts = numv(counts),
    centroids = numm(cent), centroids_standardized = numm(Z),
    pooled_covariance = numm(S),
    distances = list(
      euclidean = numm(D("euclidean")),
      sqeuclidean = numm(D("euclidean")^2),
      manhattan = numm(D("manhattan")),
      chebyshev = numm(D("maximum")),
      cosine = numm(cosd),
      mahalanobis = numm(maha)
    )
  )
}

seg <- function(n, centre, sd) sapply(centre, function(m) round(rnorm(n, m, sd), 3))
X3 <- rbind(seg(25, c(10, 50, 3), 2), seg(20, c(14, 41, 5), 3), seg(15, c(9, 60, 8), 1.5))
X3[, 2] <- round(X3[, 2] + 0.6 * X3[, 1], 3) # correlated members
g3 <- rep(1:3, c(25, 20, 15))
wf <- rep(c(1, 2, 0, 3, 1), length.out = nrow(X3))
wp <- round(runif(nrow(X3), 0.2, 3), 3)
wp[c(7, 33)] <- 0
dist_case("seeded3", X3, g3, 3, "none", FALSE)
dist_case("seeded3_standardized", X3, g3, 3, "none", TRUE)
dist_case("seeded3_frequency", X3, g3, 3, "frequency", TRUE, wf)
dist_case("seeded3_probability", X3, g3, 3, "probability", TRUE, wp)
X4 <- rbind(seg(12, c(0, 0), 1), seg(9, c(5, 1), 1.2), seg(14, c(1, 6), 0.8), seg(7, c(-4, -3), 1))
dist_case("seeded4_raw", X4, rep(1:4, c(12, 9, 14, 7)), 4, "none", FALSE)

blobs <- Filter(function(cs) cs$name == "blobs3", km_cases)[[1]]
Xb <- do.call(rbind, lapply(blobs$rows, function(r) as.numeric(unlist(r))))
gb <- as.integer(unlist(blobs$clusters))
dist_case("kmeans_blobs3", Xb, gb, 3, "none", FALSE)

dist_doc <- c(mv_meta("centroid distances (MAT_DISTANCE)",
  "weighted.mean() centroids per bucket; stats::dist() euclidean / manhattan / maximum, squared euclidean, cosine 1 - a.b/(|a||b|), sqrt(stats::mahalanobis()) over the pooled within-bucket covariance; standardize z-scores the centroids by the bucketed rows' mean and sd (scale()).",
  "Weights follow the one rule: w* = w * N*/sum(w), N* = sum(w) (frequency, asserted on the rep()-expanded rows) or Kish n_eff (probability); centroids are kind-free weighted means, the sd and the pooled covariance are frequency formulas on w*. buckets = each row's bucket number in Pulse's emission order (1-based); sizes = per-bucket sum of weights, counts = rows. mahalanobis reads the raw centroids whatever standardize says (it is affine-invariant)."),
  list(cases = dist_cases))
write_golden("mv_distance", dist_doc)
