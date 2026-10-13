# gen_matrix_inference.R — R oracle for Pulse's matrix params.inference
# (U28 E3-S2): per-pair p-values and Fisher-z confidence intervals on
# MAT_CORRELATION (pearson) and MAT_PARTIAL_CORRELATION.
#
# Writes <out_dir>/matrix_inference.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_matrix_inference.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. It reads the
# fixture CSVs gen_multivariate.R wrote under <out_dir>/fixtures, so it
# needs base R (stats) + jsonlite + ppcor (pinned 1.1 by
# gen_multivariate.R) — no new package.
#
# Weights follow Pulse's one rule (.claude/reference/weighting.md,
# "Weighted inference"), as gen_multivariate.R does: N* = sum(w) under
# frequency — and every frequency figure is stats::cor.test on the
# rep()-expanded rows, the expansion oracle — and Kish n_eff =
# sum(w)^2 / sum(w^2) under probability, where r is the weighted
# correlation (cov.wt) and t / p / the interval are cor.test's formulas
# on N*: t = r sqrt((N* - 2) / (1 - r^2)), p = 2 pt(-|t|, N* - 2),
# CI = tanh(atanh(r) -/+ qnorm(1 - alpha/2) / sqrt(N* - 3)). Unweighted
# cases are cor.test itself (and the closed form is asserted equal).
# Pairwise: each pair over its own complete rows (its own N*).
#
# Partial correlations (control "all" = every other member, or a
# control list): p on df = N* - 2 - g (ppcor::pcor / pcor.test,
# asserted unweighted listwise), the interval tanh(atanh(r) -/+
# z / sqrt(N* - 3 - g)); pairwise N* = the smallest pair n over the
# folded columns, as ppcor's n and mv_partial_correlation.json.
# Diagonals are null; NA is JSON null.

suppressPackageStartupMessages(library(jsonlite))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
    cat(txt, file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}

mi_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
mi_mat <- function(m) lapply(seq_len(nrow(m)), function(i) lapply(unname(m[i, ]), mi_num))
mi_near <- function(a, b, tol, what) {
  d <- max(abs(a - b) / (1 + abs(b)), na.rm = TRUE)
  if (!is.finite(d) || d > tol) stop(sprintf("%s: disagree by %g", what, d))
}

mi_read <- function(name) {
  read.csv(file.path(out_dir, "fixtures", paste0(name, ".csv")), comment.char = "#")
}
mi_w <- function(df, kind) {
  switch(kind, none = rep(1, nrow(df)), frequency = df$w_freq, probability = df$w_prob)
}
mi_nstar <- function(w, kind) if (kind == "probability") sum(w)^2 / sum(w^2) else sum(w)
mi_wcor <- function(x, y, w) {
  keep <- w > 0
  cov2cor(cov.wt(cbind(x, y)[keep, , drop = FALSE], wt = w[keep] / sum(w[keep]), method = "ML")$cov)[1, 2]
}

# One pair's r, p and interval, or NA where cor.test / the size rule
# leaves them undefined.
mi_pair <- function(x, y, w, kind, alpha) {
  ok <- !is.na(x) & !is.na(y)
  x <- x[ok]; y <- y[ok]; w <- w[ok]
  n <- length(x)
  if (kind == "frequency") {
    idx <- rep(seq_len(n), w)
    ct <- cor.test(x[idx], y[idx], conf.level = 1 - alpha)
    ci <- if (is.null(ct$conf.int)) c(NA, NA) else as.numeric(ct$conf.int)
    return(list(r = unname(ct$estimate), p = ct$p.value, lo = ci[1], hi = ci[2], n_star = sum(w)))
  }
  ns <- mi_nstar(w, kind)
  r <- if (kind == "none") cor(x, y) else mi_wcor(x, y, w)
  t <- r * sqrt((ns - 2) / (1 - r^2))
  p <- 2 * pt(-abs(t), ns - 2)
  z <- qnorm(1 - alpha / 2)
  lo <- NA; hi <- NA
  if (n >= 4 && ns > 3) {
    lo <- tanh(atanh(r) - z / sqrt(ns - 3)); hi <- tanh(atanh(r) + z / sqrt(ns - 3))
  }
  if (kind == "none") {
    ct <- cor.test(x, y, conf.level = 1 - alpha)
    mi_near(c(ct$p.value, ct$conf.int), c(p, lo, hi), 1e-12, "cor.test vs closed form")
  }
  list(r = r, p = p, lo = lo, hi = hi, n_star = ns)
}

mi_cases <- list()
add_pearson <- function(fx, df, members, kind, missing = "listwise", alpha = 0.05) {
  d <- if (missing == "listwise") df[complete.cases(df[, members]), , drop = FALSE] else df
  w <- mi_w(d, kind)
  k <- length(members)
  R <- matrix(NA_real_, k, k); P <- R; LO <- R; HI <- R; NS <- R
  for (i in seq_len(k)) for (j in seq_len(k)) {
    if (i == j) next
    s <- mi_pair(d[[members[i]]], d[[members[j]]], w, kind, alpha)
    R[i, j] <- s$r; P[i, j] <- s$p; LO[i, j] <- s$lo; HI[i, j] <- s$hi; NS[i, j] <- s$n_star
  }
  diag(R) <- 1
  mi_cases[[length(mi_cases) + 1]] <<- list(
    type = "MAT_CORRELATION", fixture = fx, members = I(members), controls = I(character(0)),
    missing = missing, weight = kind, alpha = mi_num(alpha),
    r = mi_mat(R), n_star = mi_mat(NS), p = mi_mat(P), ci_lower = mi_mat(LO), ci_upper = mi_mat(HI)
  )
}

add_partial <- function(fx, df, members, controls, kind, missing = "listwise", alpha = 0.05) {
  cols <- c(members, controls)
  d <- if (missing == "listwise") df[complete.cases(df[, cols]), , drop = FALSE] else df
  w <- mi_w(d, kind)
  X <- as.matrix(d[, cols])
  if (missing == "pairwise") {
    if (kind != "none") stop("pairwise partial oracle is unweighted only")
    R <- cor(X, use = "pairwise.complete.obs")
    ok <- !is.na(X)
    ns <- min(crossprod(ok * 1))
  } else {
    ns <- mi_nstar(w, kind)
    R <- if (kind == "none") cor(X) else {
      keep <- w > 0
      cov2cor(cov.wt(X[keep, , drop = FALSE], wt = w[keep] / sum(w[keep]), method = "ML")$cov)
    }
  }
  k <- length(members)
  g <- if (length(controls) == 0) k - 2 else length(controls)
  Prec <- solve(R)
  PC <- matrix(NA_real_, k, k)
  if (length(controls) == 0) {
    PC <- -Prec / sqrt(outer(diag(Prec), diag(Prec)))
  } else {
    S <- solve(Prec[1:k, 1:k])
    PC <- S / sqrt(outer(diag(S), diag(S)))
  }
  diag(PC) <- 1
  z <- qnorm(1 - alpha / 2)
  P <- matrix(NA_real_, k, k); LO <- P; HI <- P
  for (i in seq_len(k)) for (j in seq_len(k)) {
    if (i == j) next
    r <- PC[i, j]
    t <- r * sqrt((ns - 2 - g) / (1 - r^2))
    P[i, j] <- 2 * pt(-abs(t), ns - 2 - g)
    LO[i, j] <- tanh(atanh(r) - z / sqrt(ns - 3 - g))
    HI[i, j] <- tanh(atanh(r) + z / sqrt(ns - 3 - g))
  }
  if (kind == "none" && missing == "listwise") {
    if (length(controls) == 0) {
      pp <- ppcor::pcor(X)
      diag(pp$p.value) <- NA
      mi_near(pp$p.value, P, 1e-9, paste("ppcor::pcor p", fx))
    } else {
      for (i in seq_len(k)) for (j in seq_len(k)) if (i < j) {
        pt2 <- ppcor::pcor.test(X[, i], X[, j], X[, (k + 1):(k + length(controls)), drop = FALSE])
        mi_near(pt2$p.value, P[i, j], 1e-9, paste("ppcor::pcor.test p", fx))
      }
    }
  }
  mi_cases[[length(mi_cases) + 1]] <<- list(
    type = "MAT_PARTIAL_CORRELATION", fixture = fx, members = I(members), controls = I(controls),
    missing = missing, weight = kind, alpha = mi_num(alpha), n_star = mi_num(ns), gp = mi_num(g),
    r = mi_mat(PC), p = mi_mat(P), ci_lower = mi_mat(LO), ci_upper = mi_mat(HI)
  )
}

mi_att <- mi_read("attitude")
mi_mt <- mi_read("mtcars")
mi_nl <- mi_read("nulls")
mi_wd <- mi_read("weighted")
for (kind in c("none", "frequency", "probability")) {
  add_pearson("attitude", mi_att, c("rating", "complaints", "privileges", "learning", "raises", "critical", "advance"), kind)
  add_pearson("mtcars", mi_mt, c("mpg", "disp", "hp", "wt", "qsec"), kind)
  add_pearson("weighted", mi_wd, paste0("x", 1:4), kind)
  add_pearson("nulls", mi_nl, paste0("x", 1:4), kind, "pairwise")
  add_partial("attitude", mi_att, c("rating", "complaints", "privileges", "learning", "raises", "critical", "advance"), character(0), kind)
  add_partial("mtcars", mi_mt, c("mpg", "disp", "hp", "qsec"), c("wt"), kind)
  add_partial("weighted", mi_wd, paste0("x", 1:4), character(0), kind)
}
add_pearson("attitude", mi_att, c("rating", "complaints", "privileges", "learning"), "none", alpha = 0.1)
add_partial("nulls", mi_nl, paste0("x", 1:4), character(0), "none", "pairwise")
add_partial("attitude", mi_att, c("rating", "privileges", "raises", "critical", "advance"), c("complaints", "learning"), "none", alpha = 0.01)

write_golden("matrix_inference", list(
  generator = "scripts/reference/gen_matrix_inference.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite")),
                  ppcor = as.character(packageVersion("ppcor"))),
  note = "Per-pair p-values and Fisher-z intervals for MAT_CORRELATION (cor.test; frequency = rep() expansion, probability = Kish n_eff closed form) and MAT_PARTIAL_CORRELATION (df = N* - 2 - g, se = 1/sqrt(N* - 3 - g); ppcor asserted). Diagonals null.",
  cases = mi_cases
))
