# The two-step (polychoric / polyserial) edge-case datasets — committed
# CSVs, so the Go tests never need R:
#   poly_extreme_data.csv  n = 1500, seeded: ordinal a..f and numeric
#     x, y, xr — latent correlations near ±1 (0.995, 0.9999 → the 0.999
#     bound, −0.98), extreme thresholds (category shares 0.3 % / 0.9 %),
#     a binary item, a numeric × numeric pair and a polyserial pair near
#     the 0.995 bound;
#   poly_sparse_data.csv   n = 45, seeded: three 5-category items with
#     empty cells in every bivariate table;
#   poly_binary_data.csv   n = 40, constructed: 2 × 2 tables hitting each
#     of lavaan's shortcuts (both thresholds 0, one empty cell, odds
#     ratio 1, two empty cells) and a 2 × 3 table with empty cells;
#   poly_weighted_data.csv brand_data.csv's first 240 rows and five
#     columns with a frequency weight w = 1 + (row mod 3).
#   Rscript gen_polychoric_data.R   (from this directory)
cutq <- function(z, p) as.integer(cut(z, c(-Inf, qnorm(p), Inf))) # codes 1..K
set.seed(20261024)
n <- 1500
z1 <- rnorm(n); e <- matrix(rnorm(n * 8), n)
mix <- function(r, k) r * z1 + sqrt(1 - r^2) * e[, k]
pts <- c(0.1, 0.3, 0.6, 0.85)
ext <- data.frame(
  a = cutq(z1, pts),
  b = cutq(mix(0.995, 1), pts),
  c = cutq(mix(0.9999, 2), pts),
  d = cutq(mix(-0.98, 3), pts),
  e = cutq(mix(0.5, 4), c(0.003, 0.012, 0.97, 0.991)),
  f = cutq(mix(0.6, 5), 0.8),
  x = round(mix(0.9, 6), 4),
  xr = round(mix(0.9995, 7), 4))
ext$y <- round(0.7 * ext$x + 0.5 * e[, 8], 4)
stopifnot(all(sapply(ext[, c("a", "b", "c", "d", "e", "f")], function(v) all(table(v) > 0))))
write.csv(ext, "poly_extreme_data.csv", row.names = FALSE)

set.seed(7)
n <- 45
z <- rnorm(n)
sp <- data.frame(
  p = cutq(z, pts),
  q = cutq(0.6 * z + 0.8 * rnorm(n), pts),
  r = cutq(-0.4 * z + sqrt(0.84) * rnorm(n), c(0.2, 0.4, 0.6, 0.8)))
stopifnot(all(sapply(sp, function(v) length(unique(v)) == 5)))
write.csv(sp, "poly_sparse_data.csv", row.names = FALSE)

rep01 <- function(...) { s <- c(...); rep(rep(c(0L, 1L), length.out = length(s)), s) }
bin <- data.frame(
  x1 = rep01(20, 20),
  x2 = rep01(15, 5, 5, 15),
  x3 = c(rep01(20), rep01(8, 12)),
  x4 = rep01(10, 10, 10, 10),
  x5 = rep01(20, 20),
  x6 = c(rep(0L, 12), rep(1L, 8), rep(1L, 4), rep(2L, 16)))
write.csv(bin, "poly_binary_data.csv", row.names = FALSE)

br <- read.csv("brand_data.csv")[1:240, c("q1", "q2", "v1", "s1", "recommend")]
br$w <- 1L + (seq_len(nrow(br)) %% 3L)
write.csv(br, "poly_weighted_data.csv", row.names = FALSE)
