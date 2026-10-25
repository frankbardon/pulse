# The two-step ordinal sample statistics of a dataset, as lavaan computes
# them for DWLS / WLSMV (lavCor(..., ordered = ...); muthen1984), plus an
# independent re-derivation, dumped as JSON for the TwoStep parity tests
# (TestPolychoric_*):
#   Rscript fit_lavaan_polychoric.R data.csv vars ordered [group] [weight] [score_rows]
# vars is the comma-separated column order, ordered the ordinal subset
# (the rest are numeric). group names a grouping column (one block per
# level, sorted), weight a frequency-weight column: the data are then
# REPLICATED w times (lavaan sees the copies; the Go side weighs the
# rows). score_rows > 0 (ungrouped, unweighted only) also dumps the first
# score_rows rows of lavaan's casewise score matrix SC, its INNER =
# crossprod(SC) and A21 (muthen1984, lavaan's internal Gamma pieces);
# every ungrouped run dumps lavaan's Gamma pieces (gamma: A11, A21, A22,
# WLS.W, WLS.obs) for the TestWLSMV_Gamma* tests.
#
# The independent block re-derives every figure without lavaan:
# thresholds qnorm(cumulative proportions); polychorics as the root
# (uniroot, tol 1e-15) of the score Σ n·(∂π/∂ρ)/π with cell
# probabilities from an adaptive quadrature of Sheppard's integral (not
# pbivnorm), lavaan's 2 × 2 continuity rule and its cell floor
# eps^(2/3); polyserials as the root
# of Olsson's score with R's pnorm (and lavaan's floor eps on the
# conditional probability); Pearson as cov / sd·sd (ML). A
# maximum outside ±0.999 (±0.995 polyserial) is the bound; lavaan's
# two-empty-cell ±1 shortcut has no independent value (null).
suppressPackageStartupMessages({ library(lavaan); library(jsonlite) })
args <- commandArgs(trailingOnly = TRUE)
data <- read.csv(args[1])
vars <- strsplit(args[2], ",")[[1]]
ord <- strsplit(args[3], ",")[[1]]
grp <- if (length(args) >= 4 && nzchar(args[4])) args[4] else NULL
wcol <- if (length(args) >= 5 && nzchar(args[5])) args[5] else NULL
nsc <- if (length(args) >= 6 && nzchar(args[6])) as.integer(args[6]) else 0L
if (!is.null(wcol)) data <- data[rep(seq_len(nrow(data)), data[[wcol]]), , drop = FALSE]
if (!is.null(grp)) data[[grp]] <- factor(data[[grp]], levels = sort(unique(data[[grp]])))

sheppard <- function(h, k, r) {
  if (!is.finite(h) || !is.finite(k)) {
    if (h == -Inf || k == -Inf) return(0)
    if (h == Inf) return(pnorm(k))
    return(pnorm(h))
  }
  if (r == 0) return(pnorm(h) * pnorm(k))
  f <- function(t) exp(-(h^2 + k^2 - 2 * h * k * sin(t)) / (2 * cos(t)^2))
  # rel.tol 2e-14, relaxed (1e-12, 1e-10) where integrate reports
  # roundoff — far below the 1e-6 the estimates are compared at
  quad <- function(tol) integrate(f, 0, asin(r), rel.tol = tol, abs.tol = 1e-17, subdivisions = 5000L)$value
  v <- tryCatch(quad(2e-14), error = function(e) tryCatch(quad(1e-12), error = function(e) quad(1e-10)))
  pnorm(h) * pnorm(k) + v / (2 * pi)
}
dbin <- function(u, v, r) exp(-(u^2 - 2 * r * u * v + v^2) / (2 * (1 - r^2))) / (2 * pi * sqrt(1 - r^2))
indep_th <- function(y) { p <- table(y) / length(y); qnorm(cumsum(p)[-length(p)]) }
indep_polychoric <- function(y1, y2) {
  t1 <- c(-Inf, indep_th(y1), Inf); t2 <- c(-Inf, indep_th(y2), Inf)
  n <- table(factor(y1, levels = sort(unique(y1))), factor(y2, levels = sort(unique(y2))))
  n <- matrix(as.numeric(n), nrow(n))
  if (all(dim(n) == 2)) {
    z <- which(n == 0)
    if (length(z) == 2) return(NA)
    if (length(z) == 1) {
      s <- if (z %in% c(1, 4)) 1 else -1
      n <- n + 0.5 * s * matrix(c(1, -1, -1, 1), 2)
    }
  }
  score <- function(r) {
    s <- 0
    for (i in seq_len(nrow(n))) for (j in seq_len(ncol(n))) {
      if (n[i, j] == 0) next
      p <- sheppard(t1[i + 1], t2[j + 1], r) - sheppard(t1[i], t2[j + 1], r) -
        sheppard(t1[i + 1], t2[j], r) + sheppard(t1[i], t2[j], r)
      p <- max(p, .Machine$double.eps^(2/3)) # lavaan's floor (near the bound)
      dd <- function(a, b) if (is.finite(a) && is.finite(b)) dbin(a, b, r) else 0
      d <- dd(t1[i + 1], t2[j + 1]) - dd(t1[i], t2[j + 1]) - dd(t1[i + 1], t2[j]) + dd(t1[i], t2[j])
      s <- s + n[i, j] * d / p
    }
    s
  }
  root(score, 0.999)
}
root <- function(score, b) {
  lo <- score(-b); hi <- score(b)
  if (lo <= 0) return(-b)
  if (hi >= 0) return(b)
  uniroot(score, c(-b, b), tol = 1e-15, maxiter = 1000)$root
}
indep_polyserial <- function(x, y) {
  th <- c(-100, indep_th(y), 100)
  k <- as.integer(factor(y, levels = sort(unique(y))))
  z <- (x - mean(x)) / sqrt(mean((x - mean(x))^2))
  up <- th[k + 1]; lo <- th[k]
  score <- function(r) {
    s <- sqrt(1 - r^2)
    a <- (up - r * z) / s; b <- (lo - r * z) / s
    pr <- ifelse(b > 0, pnorm(-b) - pnorm(-a), pnorm(a) - pnorm(b))
    pr <- pmax(pr, .Machine$double.eps) # lavaan's floor (near the bound)
    sum((dnorm(a) * (up * r - z) - dnorm(b) * (lo * r - z)) / (pr * s^3))
  }
  root(score, 0.995)
}
indep_block <- function(d) {
  out <- list(thresholds = list(), cor = matrix(diag(length(vars)), length(vars)))
  for (v in ord) out$thresholds[[v]] <- I(unname(indep_th(d[[v]])))
  for (j in seq_along(vars)) for (i in seq_along(vars)) if (i > j) {
    a <- vars[j]; b <- vars[i]
    r <- if ((a %in% ord) && (b %in% ord)) indep_polychoric(d[[a]], d[[b]])
         else if (a %in% ord) indep_polyserial(d[[b]], d[[a]])
         else if (b %in% ord) indep_polyserial(d[[a]], d[[b]])
         else { x <- d[[a]] - mean(d[[a]]); y <- d[[b]] - mean(d[[b]]); mean(x * y) / sqrt(mean(x^2) * mean(y^2)) }
    out$cor[i, j] <- out$cor[j, i] <- r
  }
  out$cor <- unname(lapply(seq_len(nrow(out$cor)), function(i) out$cor[i, ]))
  out
}

fit <- lavCor(data[, c(vars, grp), drop = FALSE], ordered = ord, group = grp, output = "fit")
ss <- lavInspect(fit, "sampstat")
if (is.null(grp)) ss <- list(ss)
labels <- if (is.null(grp)) "" else lavInspect(fit, "group.label")
block <- function(g) {
  s <- ss[[g]]
  cv <- s$cov[vars, vars]
  cr <- cov2cor(cv)
  th <- list(); for (v in ord) th[[v]] <- I(unname(s$th[grep(paste0("^", v, "\\|t"), names(s$th))]))
  num <- setdiff(vars, ord)
  d <- if (is.null(grp)) data else data[data[[grp]] == labels[g], , drop = FALSE]
  list(label = labels[g], n = nrow(d), thresholds = th,
       mean = as.list(s$mean[num]), var = as.list(diag(cv)[num]),
       cor = unname(lapply(seq_len(nrow(cr)), function(i) unname(cr[i, ]))),
       independent = indep_block(d))
}
out <- list(
  lavaan = as.character(packageVersion("lavaan")),
  r = R.version.string,
  options = list(vars = vars, ordered = ord, group = grp, weight = wcol,
                 zero_add = lavInspect(fit, "options")$zero.add,
                 zero_keep_margins = lavInspect(fit, "options")$zero.keep.margins),
  groups = lapply(seq_along(ss), block))
if (is.null(grp)) {
  # muthen1984 on the (replicated, when weighted) rows: lavaan's own
  # Gamma pieces, the default numeric_bread = "observed" of sem() /
  # cfa(): A11 (per-variable blocks; a numeric column's the observed
  # information), A21 and A22 (a pair touching a numeric column: the
  # observed second derivatives), WLS.W (the asymptotic covariance of
  # WLS.obs: the cor -> cov conversion H and the numeric-mean sign flip
  # applied; NACOV = N * WLS.W) and WLS.obs itself (thresholds, a
  # numeric column's -mean, numeric variances, then the off-diagonal
  # covariances, lav_samp_wls_obs's layout).
  y <- as.matrix(data[, vars])
  for (v in ord) y[, v] <- as.integer(ordered(y[, v]))
  types <- ifelse(vars %in% ord, "ordered", "numeric")
  lev <- sapply(vars, function(v) if (v %in% ord) length(unique(y[, v])) else 0L)
  m <- lavaan:::muthen1984(data_1 = y, ov_names = vars, ov_types = types, ov_levels = unname(lev))
  rows <- function(x) unname(lapply(seq_len(nrow(x)), function(i) unname(x[i, ])))
  if (nsc > 0 && is.null(wcol)) {
    out$scores <- list(rows = rows(m$SC[seq_len(nsc), , drop = FALSE]), inner = rows(m$INNER), a21 = rows(m$A21))
  }
  th <- unlist(m$TH)
  th[unlist(m$TH.IDX) == 0] <- -th[unlist(m$TH.IDX) == 0]
  num <- which(types == "numeric")
  wobs <- c(th, unlist(m$VAR)[num], lavaan:::lav_mat_vech(m$COV, diagonal = FALSE))
  out$gamma <- list(n = nrow(y), a11 = rows(m$A11), a21 = rows(m$A21), a22 = unname(diag(m$A22)),
                    wls_w = rows(m$WLS.W), wls_obs = unname(wobs))
}
cat(toJSON(out, auto_unbox = TRUE, digits = I(17), pretty = FALSE, na = "null", null = "null"), "\n")
