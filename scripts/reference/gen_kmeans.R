# gen_kmeans.R — R oracle for Pulse's GROUP_KMEANS (U25 E4-S1).
#
# Sourced by gen_multivariate.R (`make reference`) after its fixtures and
# helpers exist: it reuses out_dir, num(), numv(), numm(), strs(),
# near(), write_golden(), mv_meta() and the attitude fixture (att).
# Writes <out_dir>/mv_kmeans.json. CI never runs R: everything here is
# committed. Base R (stats) only — no new package pin.
#
# Pulse seeds k-means++ from its own PCG stream, which stats::kmeans
# cannot reproduce, so the oracle pins what is seed-free:
#   * Separable cases (seeded blobs, written into the golden itself so no
#     shared fixture moves): stats::kmeans(Z, centers = <one row per
#     blob>, algorithm = "Lloyd") from FIXED starts converges to the one
#     optimal partition every reasonable start reaches. Z = scale(X)
#     (ddof = 1, R's scale()) when standardize, else X; a frequency case
#     clusters the rep()-expanded rows. Clusters are renumbered as Pulse
#     numbers them — descending size, ties by the lexicographic Z
#     centroid — and the golden carries, per cluster, size, within-SS,
#     the Z centroid and the original-unit centroid, plus totss /
#     betweenss and every row's cluster number.
#   * A non-separable case (datasets::attitude, k = 3): the best
#     tot.withinss stats::kmeans finds over nstart = 200 Hartigan-Wong
#     starts (set.seed) — Pulse's within-SS must not exceed it.

set.seed(901)
blob <- function(n, centre, sd) {
  sapply(centre, function(m) round(rnorm(n, m, sd), 3))
}
km_cases <- list()
km_case <- function(name, X, k, standardize, starts, w = NULL) {
  X <- as.matrix(X)
  colnames(X) <- paste0("x", seq_len(ncol(X)))
  wv <- if (is.null(w)) rep(1, nrow(X)) else w
  E <- X[rep(seq_len(nrow(X)), wv), , drop = FALSE]
  if (standardize) {
    ctr <- colMeans(E)
    scl <- apply(E, 2, sd)
  } else {
    ctr <- rep(0, ncol(X))
    scl <- rep(1, ncol(X))
  }
  Z <- sweep(sweep(X, 2, ctr, "-"), 2, scl, "/")
  ZE <- Z[rep(seq_len(nrow(Z)), wv), , drop = FALSE]
  fit <- stats::kmeans(ZE, centers = Z[starts, , drop = FALSE], algorithm = "Lloyd", iter.max = 100)
  # Row cluster on the unexpanded rows: the expansion keeps row order.
  cl <- fit$cluster[cumsum(wv)]
  size <- as.numeric(tapply(rep(1, nrow(ZE)), factor(fit$cluster, levels = seq_len(k)), sum))
  ord <- order(-size, fit$centers[, 1], if (ncol(Z) > 1) fit$centers[, 2] else 0)
  relabel <- match(seq_len(k), ord)
  cz <- fit$centers[ord, , drop = FALSE]
  co <- sweep(sweep(cz, 2, scl, "*"), 2, ctr, "+")
  # Self-check: the Lloyd fixed point — every centroid is its cluster's
  # mean, every row is nearest its own centroid.
  for (j in seq_len(k)) near(colMeans(ZE[fit$cluster == ord[j], , drop = FALSE]), cz[j, ], 1e-12, paste(name, "centroid", j))
  d <- sapply(seq_len(k), function(j) rowSums(sweep(Z, 2, cz[j, ], "-")^2))
  if (any(apply(d, 1, which.min) != relabel[cl])) stop("self-check failed: ", name, " not a fixed point")
  km_cases[[length(km_cases) + 1]] <<- list(
    name = name, kind = "separable", k = num(k), standardize = standardize,
    weight = if (is.null(w)) "none" else "frequency",
    rows = numm(X), weights = if (is.null(w)) jnull else numv(w),
    sizes = numv(size[ord]), within_ss = numv(fit$withinss[ord]),
    total_ss = num(fit$totss), between_ss = num(fit$betweenss),
    centroids_standardized = numm(cz), centroids = numm(co),
    clusters = numv(relabel[cl]), r_best_within_ss = jnull
  )
}

b3 <- rbind(blob(25, c(0, 0), 0.4), blob(15, c(6, 1), 0.5), blob(10, c(2, 7), 0.3))
km_case("blobs3", b3, 3, TRUE, c(1, 26, 41))
b4 <- rbind(blob(30, c(0, 0, 0), 0.5), blob(20, c(10, 0, 5), 0.6),
            blob(12, c(0, 12, -4), 0.4), blob(8, c(-9, -9, 9), 0.5))
km_case("blobs4_raw", b4, 4, FALSE, c(1, 31, 51, 63))
w3 <- rep(c(1, 2, 3), length.out = nrow(b3))
km_case("blobs3_frequency", b3, 3, TRUE, c(1, 26, 41), w = w3)

attn <- names(datasets::attitude)
Xa <- as.matrix(att[, attn])
colnames(Xa) <- paste0("x", seq_along(attn))
set.seed(902)
best <- stats::kmeans(scale(Xa), centers = 3, nstart = 200)
km_cases[[length(km_cases) + 1]] <- list(
  name = "attitude_k3", kind = "best_of_starts", k = num(3), standardize = TRUE, weight = "none",
  rows = numm(Xa), weights = jnull, sizes = jnull, within_ss = jnull, total_ss = num(best$totss),
  between_ss = jnull, centroids_standardized = jnull, centroids = jnull, clusters = jnull,
  r_best_within_ss = num(best$tot.withinss)
)

km_doc <- c(mv_meta("k-means segmentation (GROUP_KMEANS)",
  "stats::kmeans(Z, centers = fixed starts, algorithm = 'Lloyd', iter.max = 100) on separable seeded blobs (Z = scale(X) when standardize; frequency: the rep()-expanded rows), self-checked as a Lloyd fixed point; attitude: the best tot.withinss of stats::kmeans(scale(X), 3, nstart = 200).",
  "Separable cases: clusters renumbered as Pulse labels them (descending size, ties by the lexicographic standardized centroid); sizes / within_ss / centroids in that order, centroids in original units, centroids_standardized in the clustering space; clusters = each row's cluster number (1-based, row order); total_ss / between_ss in the clustering space. best_of_starts: Pulse's within-SS must be at most r_best_within_ss."),
  list(cases = km_cases))
write_golden("mv_kmeans", km_doc)
