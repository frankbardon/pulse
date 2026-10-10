# gen_multivariate.R — R oracle for Pulse's multivariate matrix outputs
# (U24 matrix operators: rank / partial correlation, reliability, the
# one-factor minres solver, PCA + KMO + Bartlett, collinearity, the
# regression covariance matrix and the nearest-correlation repair).
#
# Sourced by gen_reference.R (`make reference`), which provides out_dir,
# num() and write_golden(). Writes:
#   <out_dir>/fixtures/<name>.csv   vendored + seeded fixtures (the
#                                   refcohorts tool then writes <name>.pulse)
#   <out_dir>/mv_*.json             one golden per method family
# CI never runs R: everything here is committed. A rerun on the pinned
# toolchain is byte-identical (no clock, no unseeded RNG, sorted output).
#
# Pinned packages (the script REFUSES to run on any other version, so a
# regenerated golden can never silently move with a CRAN update):
#   R 4.6.1, stats 4.6.1, jsonlite 2.0.0, Matrix 1.7-5,
#   psych 2.6.9, car 3.1-5, ppcor 1.1, perturb 2.10, corpcor 1.6.10
# Install into the user library (perturb is archived on CRAN; it needs
# gdata):
#   Rscript -e 'r <- "https://cloud.r-project.org"; l <- Sys.getenv("R_LIBS_USER");
#     install.packages(c("psych","car","ppcor","corpcor","gdata"), lib = l, repos = r);
#     install.packages(paste0(r, "/src/contrib/Archive/perturb/perturb_2.10.tar.gz"),
#                      lib = l, repos = NULL, type = "source")'
# On Homebrew R, if Fortran packages fail to link (`library 'emutls_w'
# not found`), point R_MAKEVARS_USER at a Makevars whose FLIBS names the
# installed gcc's real lib/gcc/current/gcc/<triple>/<ver> directory.
#
# Fixtures (all public domain or seeded; NO GPL-package data such as
# psych::bfi):
#   attitude        datasets::attitude (30 x 7). Chatterjee, S. & Price,
#                   B. (1977) Regression Analysis by Example. Wiley.
#   mtcars          datasets::mtcars (32 x 11). Henderson, H. V. &
#                   Velleman, P. F. (1981) Building multiple regression
#                   models interactively. Biometrics 37, 391-411.
#   likert_ties     seeded Likert 1-5 battery (heavy ties)
#   nulls           seeded continuous block with scattered nulls
#                   (listwise vs pairwise)
#   weighted        seeded block with frequency + probability weights,
#                   zero-weight rows included, plus lm / glm responses
#   nonpsd_pairwise seeded three-block design whose PAIRWISE Pearson
#                   matrix is not positive semi-definite
#   heywood         seeded draw from a one-factor-implied loading > 1
#                   (Heywood / ultra-Heywood under minres)
#   reversed_items  seeded Likert 1-5 battery with two reverse-keyed items
# The two vendored fixtures gain two SEEDED weight columns (w_freq,
# w_prob) that are not part of the original data; the header says so.
#
# Conventions every consumer relies on:
#   * Weights follow Pulse's one rule (.claude/reference/weighting.md,
#     "Weighted inference"): N* = sum(w) under frequency, Kish n_eff =
#     sum(w)^2 / sum(w^2) under probability, and every weighted formula is
#     the frequency formula on w* = w * N* / sum(w). Covariances come from
#     stats::cov.wt: frequency = ML * N*/(N*-1) (== cov of the rep()-
#     expanded rows, asserted), probability = method "unbiased" (which IS
#     the frequency formula on w*). Rank methods take frequency weights
#     only and are computed on the rep()-expanded rows.
#   * Eigenvectors are reported UNSORTED-BY-SIGN as R returns them, then
#     put through Pulse's convention (linalg SymEigen): each vector is
#     flipped so its largest-magnitude component is positive, a tie within
#     2^-26 relative going to the lowest index.
#   * Factor loadings are signed so sum(lambda) > 0.
#   * NA in a golden is JSON null (an undefined figure); Inf is refused.

pinned <- c(
  jsonlite = "2.0.0", Matrix = "1.7.5", psych = "2.6.9", car = "3.1.5",
  ppcor = "1.1", perturb = "2.10", corpcor = "1.6.10"
)
for (p in names(pinned)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != pinned[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, pinned[[p]]))
  }
}
if (R.version.string != "R version 4.6.1 (2026-06-24)") stop("the oracle is pinned to R 4.6.1, got ", R.version.string)

suppressPackageStartupMessages({
  library(psych)
  library(car)
  library(ppcor)
  library(perturb)
  library(corpcor)
  library(Matrix)
})

RNGkind("Mersenne-Twister", "Inversion", "Rejection")

fix_dir <- file.path(out_dir, "fixtures")
dir.create(fix_dir, recursive = TRUE, showWarnings = FALSE)

# ------------------------------------------------------------ JSON helpers
jnull <- structure("null", class = "json")
numx <- function(x) {
  if (length(x) != 1) stop("numx() takes a scalar")
  if (is.na(x)) return(jnull)
  num(x)
}
numv <- function(x) lapply(unname(as.numeric(x)), numx)
numm <- function(m) {
  m <- as.matrix(m)
  lapply(seq_len(nrow(m)), function(i) numv(m[i, ]))
}
strs <- function(x) I(unname(as.character(x)))

mv_meta <- function(family, r_fn, note) {
  list(
    generator = "scripts/reference/gen_multivariate.R (via gen_reference.R)",
    r_version = R.version.string,
    packages = c(list(stats = as.character(packageVersion("stats"))),
                 lapply(as.list(pinned), function(v) v)),
    primitive = family,
    r_function = r_fn,
    note = note
  )
}

near <- function(a, b, tol = 1e-10, what = "") {
  a <- as.numeric(a); b <- as.numeric(b)
  ok <- (is.na(a) & is.na(b)) | (abs(a - b) <= tol * pmax(1, abs(a), abs(b)))
  if (!all(ok)) stop("self-check failed: ", what, " max diff ", max(abs(a - b), na.rm = TRUE))
}

# --------------------------------------------------------------- fixtures
fixtures <- list()
write_fixture <- function(name, df, source, citation, seed, note) {
  path <- file.path(fix_dir, paste0(name, ".csv"))
  cell <- function(v) ifelse(is.na(v), "", sprintf("%.17g", v))
  hdr <- c(
    sprintf("# %s — Pulse R-oracle fixture (scripts/reference/gen_multivariate.R)", name),
    sprintf("# source: %s", source),
    sprintf("# citation: %s", citation),
    sprintf("# note: %s", note),
    "# format: %.17g decimals; an empty cell is null"
  )
  body <- vapply(seq_len(nrow(df)), function(i) {
    paste(vapply(df[i, , drop = TRUE], function(v) cell(as.numeric(v)), ""), collapse = ",")
  }, "")
  con <- file(path, open = "wb")
  writeLines(c(hdr, paste(names(df), collapse = ","), body), con, sep = "\n")
  close(con)
  fixtures[[length(fixtures) + 1]] <<- list(
    name = name, source = source, citation = citation,
    seed = if (is.null(seed)) jnull else num(seed),
    rows = num(nrow(df)), columns = strs(names(df)), note = note,
    csv_md5 = unname(tools::md5sum(path))
  )
  message("wrote ", path)
  df
}

seeded_weights <- function(n, seed, zeros = FALSE) {
  set.seed(seed)
  wf <- sample(1:4, n, replace = TRUE)
  wp <- round(runif(n, 0.2, 3), 3)
  if (zeros) {
    z <- c(3, 11, 27)
    z <- z[z <= n]
    wf[z] <- 0
    wp[z[-1]] <- 0
  }
  list(w_freq = wf, w_prob = wp)
}

att <- datasets::attitude
w <- seeded_weights(nrow(att), 101)
att$w_freq <- w$w_freq; att$w_prob <- w$w_prob
att <- write_fixture("attitude", att, "datasets::attitude (R)",
  "Chatterjee, S. & Price, B. (1977) Regression Analysis by Example. New York: Wiley.",
  101, "30 x 7 as shipped with R; w_freq / w_prob are SEEDED weights appended by the generator, not part of the data.")

mt <- datasets::mtcars
rownames(mt) <- NULL
w <- seeded_weights(nrow(mt), 102)
mt$w_freq <- w$w_freq; mt$w_prob <- w$w_prob
mt <- write_fixture("mtcars", mt, "datasets::mtcars (R)",
  "Henderson, H. V. & Velleman, P. F. (1981) Building multiple regression models interactively. Biometrics 37, 391-411.",
  102, "32 x 11 as shipped with R (row names dropped); w_freq / w_prob are SEEDED weights appended by the generator, not part of the data.")

likert <- function(n, k, load, seed) {
  set.seed(seed)
  f <- rnorm(n)
  m <- sapply(seq_len(k), function(j) pmin(5, pmax(1, round(3 + load[j] * f + rnorm(n, 0, 0.9)))))
  m
}

lk <- as.data.frame(likert(60, 6, c(0.9, 0.8, 1.0, 0.6, 0.7, 0.5), 201))
names(lk) <- paste0("q", 1:6)
w <- seeded_weights(60, 202)
lk$w_freq <- w$w_freq; lk$w_prob <- w$w_prob
lk <- write_fixture("likert_ties", lk, "synthetic", "none (seeded)", 201,
  "six 1-5 Likert items on one latent factor; heavy ties for rank methods and alpha.")

set.seed(301)
nl_n <- 48
Lc <- chol(matrix(c(1, .6, .4, .2, .6, 1, .5, .3, .4, .5, 1, .45, .2, .3, .45, 1), 4))
nl <- as.data.frame(matrix(rnorm(nl_n * 4), nl_n) %*% Lc)
names(nl) <- paste0("x", 1:4)
nl$x1 <- round(10 + 2 * nl$x1, 6); nl$x2 <- round(50 + 8 * nl$x2, 6)
nl$x3 <- round(nl$x3, 6); nl$x4 <- round(100 * nl$x4, 6)
for (j in 1:4) nl[sample(nl_n, 6), j] <- NA
w <- seeded_weights(nl_n, 302)
nl$w_freq <- w$w_freq; nl$w_prob <- w$w_prob
nl <- write_fixture("nulls", nl, "synthetic", "none (seeded)", 301,
  "four correlated continuous fields, 6 nulls each at seeded rows: listwise and pairwise differ.")

set.seed(401)
wt_n <- 40
Lw <- chol(matrix(c(1, .5, .3, .1, .5, 1, .4, .2, .3, .4, 1, .3, .1, .2, .3, 1), 4))
wd <- as.data.frame(round(matrix(rnorm(wt_n * 4), wt_n) %*% Lw, 6))
names(wd) <- paste0("x", 1:4)
wd$y <- round(1 + 0.8 * wd$x1 - 0.5 * wd$x2 + 0.3 * wd$x4 + rnorm(wt_n, 0, 0.7), 6)
wd$y_bin <- as.numeric(runif(wt_n) < plogis(0.2 + 0.9 * wd$x1 - 0.6 * wd$x3))
wd$y_count <- rpois(wt_n, exp(0.4 + 0.3 * wd$x2 + 0.2 * wd$x4))
w <- seeded_weights(wt_n, 402, zeros = TRUE)
wd$w_freq <- w$w_freq; wd$w_prob <- w$w_prob
wd <- write_fixture("weighted", wd, "synthetic", "none (seeded)", 401,
  "four predictors, a continuous / binary / count response, integer frequency weights and fractional probability weights, both with zero-weight rows.")

set.seed(501)
bk <- 20
t1 <- rnorm(bk); t2 <- rnorm(bk); t3 <- rnorm(bk)
np <- data.frame(
  x = round(c(t1, rep(NA, bk), t3), 6),
  y = round(c(t1 + rnorm(bk, 0, .3), t2, rep(NA, bk)), 6),
  z = round(c(rep(NA, bk), t2 + rnorm(bk, 0, .3), -t3 + rnorm(bk, 0, .3)), 6)
)
np <- write_fixture("nonpsd_pairwise", np, "synthetic", "none (seeded)", 501,
  "three blocks each observing one pair: r(x,y) ~ +1, r(y,z) ~ +1, r(x,z) ~ -1, so the pairwise Pearson matrix has a negative eigenvalue.")

set.seed(601)
hw_P <- matrix(c(1, .8, .8, .8, 1, .5, .8, .5, 1), 3)
hw <- as.data.frame(round(matrix(rnorm(150 * 3), 150) %*% chol(hw_P), 6))
names(hw) <- c("h1", "h2", "h3")
hw <- write_fixture("heywood", hw, "synthetic", "none (seeded)", 601,
  "draws from a correlation matrix whose one-factor solution needs lambda_1^2 = .8*.8/.5 = 1.28 > 1: a Heywood case under minres.")

rv <- as.data.frame(likert(60, 5, c(0.9, 0.8, 0.9, 0.7, 0.8), 701))
names(rv) <- paste0("r", 1:5)
rv$r2 <- 6 - rv$r2
rv$r4 <- 6 - rv$r4
w <- seeded_weights(60, 702)
rv$w_freq <- w$w_freq; rv$w_prob <- w$w_prob
rv <- write_fixture("reversed_items", rv, "synthetic", "none (seeded)", 701,
  "five 1-5 Likert items, r2 and r4 reverse-keyed as stored (x' = 1 + 5 - x restores them).")
for (j in c("r1", "r2", "r3", "r4", "r5")) {
  if (min(rv[[j]]) != 1 || max(rv[[j]]) != 5) stop("reversed_items: ", j, " must span 1..5 so psych's observed-range reversal equals the declared 1..5")
}

fixture_doc <- c(mv_meta("fixtures", "datasets::attitude, datasets::mtcars, set.seed() draws",
  "Every fixture the multivariate goldens reference. csv_md5 pins the committed CSV; the .pulse twin is written from it by internal/tools/refcohorts."),
  list(cases = fixtures))

# ----------------------------------------------------------- weight utils
w_kinds <- c("none", "frequency", "probability")
wvec <- function(df, kind) {
  switch(kind, none = rep(1, nrow(df)), frequency = df$w_freq, probability = df$w_prob)
}
n_star <- function(w, kind) {
  if (kind == "probability") sum(w)^2 / sum(w^2) else sum(w)
}
# Weighted covariance under the one rule (frequency formula on w*).
wcov <- function(X, w, kind) {
  X <- as.matrix(X)
  if (kind == "none") return(cov(X))
  keep <- w > 0
  if (kind == "probability") return(cov.wt(X[keep, , drop = FALSE], wt = w[keep] / sum(w), method = "unbiased")$cov)
  S <- cov.wt(X[keep, , drop = FALSE], wt = w[keep] / sum(w), method = "ML")$cov * sum(w) / (sum(w) - 1)
  near(S, cov(X[rep(seq_len(nrow(X)), w), , drop = FALSE]), 1e-10, "frequency cov.wt vs rep()")
  S
}
wcor <- function(X, w, kind) cov2cor(wcov(X, w, kind))
wmean <- function(X, w) colSums(as.matrix(X) * w) / sum(w)
expand <- function(df, w) df[rep(seq_len(nrow(df)), w), , drop = FALSE]

sign_fix <- function(V) {
  V <- as.matrix(V)
  for (j in seq_len(ncol(V))) {
    a <- abs(V[, j]); mx <- max(a)
    k <- which(a >= mx * (1 - 2^-26))[1]
    if (V[k, j] < 0) V[, j] <- -V[, j]
  }
  V
}

listwise <- function(df, cols) {
  cc <- complete.cases(df[, cols])
  df[cc, , drop = FALSE]
}
pair_n <- function(X) {
  ok <- !is.na(as.matrix(X))
  crossprod(ok * 1)
}

# -------------------------------------------------------- rank correlation
rank_cases <- list()
add_rank <- function(fx, df, cols, missing, kind) {
  d <- if (missing == "listwise") listwise(df, cols) else df
  if (kind == "frequency") d <- expand(d, d$w_freq)
  X <- as.matrix(d[, cols])
  use <- if (missing == "listwise") "everything" else "pairwise.complete.obs"
  sp <- cor(X, method = "spearman", use = use)
  kd <- cor(X, method = "kendall", use = use)
  pr <- cor(X, method = "pearson", use = use)
  rank_cases[[length(rank_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), missing = missing, weight = kind,
    n_rows = numx(if (missing == "listwise") nrow(X) else NA_real_),
    pair_n = numm(pair_n(X)),
    spearman = numm(sp), kendall_tau_b = numm(kd), pearson = numm(pr)
  )
}
for (kind in c("none", "frequency")) {
  add_rank("attitude", att, names(datasets::attitude), "listwise", kind)
  add_rank("mtcars", mt, names(datasets::mtcars), "listwise", kind)
  add_rank("likert_ties", lk, paste0("q", 1:6), "listwise", kind)
  add_rank("weighted", wd, c(paste0("x", 1:4), "y"), "listwise", kind)
  for (m in c("listwise", "pairwise")) add_rank("nulls", nl, paste0("x", 1:4), m, kind)
}
rank_doc <- c(mv_meta("rank correlation matrices",
  "cor(X, method = 'spearman' | 'kendall' | 'pearson', use = 'everything' | 'pairwise.complete.obs')",
  "kendall is tau-b. weight 'frequency' = the rep()-expanded rows; probability weights are refused for rank methods and have no case. Pairwise re-ranks each pair over that pair's rows; pair_n counts the (expanded) rows per pair. n_rows is null under pairwise."),
  list(cases = rank_cases))

# ---------------------------------------------------- partial correlation
pcor_cases <- list()
pcor_p <- function(r, n, g) {
  stat <- r * sqrt((n - 2 - g) / (1 - r^2))
  list(stat = stat, p = 2 * pt(-abs(stat), n - 2 - g))
}
add_pcor <- function(fx, df, members, controls, kind, missing = "listwise") {
  cols <- c(members, controls)
  d <- if (missing == "listwise") listwise(df, cols) else df
  w <- wvec(d, kind)
  ns <- n_star(w, kind)
  X <- as.matrix(d[, cols])
  if (missing == "pairwise") {
    R <- cor(X, use = "pairwise.complete.obs")
    ns <- min(pair_n(X))
  } else {
    R <- wcor(X, w, kind)
  }
  k <- length(members)
  P <- matrix(NA_real_, k, k); ST <- P; PV <- P
  g <- if (length(controls) == 0) k - 2 else length(controls)
  if (length(controls) == 0) {
    full <- corpcor::cor2pcor(R)
    P <- full
    if (kind == "none" && missing == "listwise") {
      pp <- ppcor::pcor(X)
      near(pp$estimate, P, 1e-10, paste("ppcor::pcor", fx))
    }
  } else {
    for (i in seq_len(k)) for (j in seq_len(k)) {
      if (i == j) { P[i, j] <- 1; next }
      idx <- c(i, j, (k + 1):(k + length(controls)))
      P[i, j] <- corpcor::cor2pcor(R[idx, idx])[1, 2]
      if (kind == "none" && missing == "listwise" && i < j) {
        pt <- ppcor::pcor.test(X[, i], X[, j], X[, (k + 1):(k + length(controls)), drop = FALSE])
        near(pt$estimate, P[i, j], 1e-10, paste("ppcor::pcor.test", fx))
      }
    }
  }
  for (i in seq_len(k)) for (j in seq_len(k)) {
    if (i == j) next
    s <- pcor_p(P[i, j], ns, g)
    ST[i, j] <- s$stat; PV[i, j] <- s$p
  }
  if (kind == "none" && missing == "listwise" && length(controls) == 0) {
    pp <- ppcor::pcor(X)
    diag(pp$statistic) <- NA; diag(pp$p.value) <- NA
    near(pp$statistic, ST, 1e-9, "ppcor statistic"); near(pp$p.value, PV, 1e-9, "ppcor p")
  }
  pcor_cases[[length(pcor_cases) + 1]] <<- list(
    fixture = fx, members = strs(members), controls = strs(controls),
    missing = missing, weight = kind, n_rows = num(nrow(X)), n_star = num(ns),
    gp = num(g), input_correlation = numm(R), partial = numm(P),
    statistic = numm(ST), p_value = numm(PV)
  )
}
for (kind in w_kinds) {
  add_pcor("attitude", att, names(datasets::attitude), character(0), kind)
  add_pcor("attitude", att, c("rating", "privileges", "raises", "critical", "advance"), c("complaints", "learning"), kind)
  add_pcor("mtcars", mt, c("mpg", "disp", "hp", "wt", "qsec"), character(0), kind)
  add_pcor("mtcars", mt, c("mpg", "disp", "hp", "qsec"), c("wt"), kind)
  add_pcor("weighted", wd, paste0("x", 1:4), character(0), kind)
  add_pcor("nulls", nl, paste0("x", 1:4), character(0), kind)
}
add_pcor("nulls", nl, paste0("x", 1:4), character(0), "none", "pairwise")
pcor_doc <- c(mv_meta("partial correlation matrices",
  "ppcor::pcor(X) / ppcor::pcor.test(x, y, Z) (unweighted, asserted); corpcor::cor2pcor(R) on the (weighted) correlation",
  "controls empty = control for every other member (precision matrix). A named control list gives each member pair controlled for exactly those fields (the 'partial' matrix covers members only). statistic = r*sqrt((n-2-gp)/(1-r^2)), p = 2*pt(-|t|, n-2-gp) with n = n_star (sum(w) frequency, Kish n_eff probability, min pair n pairwise), as ppcor does. Diagonals of statistic / p_value are null."),
  list(cases = pcor_cases))

# --------------------------------------------- reliability + minres factor
alpha_manual <- function(C, R = cov2cor(C)) {
  p <- ncol(C)
  alpha <- function(C) { q <- ncol(C); q / (q - 1) * (1 - sum(diag(C)) / sum(C)) }
  rbar <- (sum(R) - p) / (p * (p - 1))
  rdrop <- sapply(seq_len(p), function(i) (sum(C[i, ]) - C[i, i]) / sqrt(C[i, i] * sum(C[-i, -i])))
  aid <- if (p <= 2) rep(NA_real_, p) else sapply(seq_len(p), function(i) alpha(C[-i, -i, drop = FALSE]))
  list(alpha = alpha(C), alpha_std = p * rbar / (1 + (p - 1) * rbar), rbar = rbar,
       r_drop = rdrop, alpha_if_deleted = aid, R = R)
}

fa1 <- function(R, n) {
  warns <- character(0)
  f <- withCallingHandlers(
    psych::fa(r = R, nfactors = 1, fm = "minres", n.obs = n, rotate = "none", warnings = TRUE),
    warning = function(w) { warns <<- c(warns, conditionMessage(w)); invokeRestart("muffleWarning") },
    message = function(m) { warns <<- c(warns, conditionMessage(m)); invokeRestart("muffleMessage") }
  )
  lam <- as.numeric(f$loadings[, 1])
  if (sum(lam) < 0) lam <- -lam
  psi <- as.numeric(f$uniquenesses)
  sl <- sum(lam); sp <- sum(psi)
  list(
    loadings = lam, uniquenesses = psi, communalities = as.numeric(f$communality),
    sum_loadings = sl, sum_uniquenesses = sp, sum_r = sum(R),
    omega_total_model = sl^2 / (sl^2 + sp),
    omega_total_observed = 1 - sp / sum(R),
    heywood = any(psi <= 0.005 + 1e-12) || any(abs(lam) >= 1),
    smc = as.numeric(psych::smc(R)),
    warnings = strs(sort(unique(trimws(warns))))
  )
}

rel_cases <- list()
fa_cases <- list()
add_rel <- function(fx, df, items, kind, missing = "listwise", reverse = character(0), smin = NA_real_, smax = NA_real_) {
  d <- if (missing == "listwise") listwise(df, items) else df
  X <- as.matrix(d[, items])
  for (r in reverse) X[, r] <- smin + smax - X[, r]
  w <- wvec(d, kind)
  ns <- n_star(w, kind)
  C <- if (missing == "pairwise") cov(X, use = "pairwise.complete.obs") else wcov(X, w, kind)
  # Pairwise: psych takes R from cor(use = "pairwise"), which is NOT
  # cov2cor of the pairwise covariance (each pair has its own rows).
  m <- if (missing == "pairwise") alpha_manual(C, cor(X, use = "pairwise.complete.obs")) else alpha_manual(C)
  if (kind == "none") {
    a <- suppressWarnings(suppressMessages(psych::alpha(as.data.frame(X), check.keys = FALSE, warnings = FALSE)))
    near(a$total$raw_alpha, m$alpha, 1e-10, paste("psych alpha", fx))
    near(a$total$std.alpha, m$alpha_std, 1e-10, paste("psych std alpha", fx))
    if (length(items) > 2) near(a$alpha.drop$raw_alpha, m$alpha_if_deleted, 1e-10, paste("psych alpha.drop", fx))
    if (missing == "listwise") near(a$item.stats$r.drop, m$r_drop, 1e-10, paste("psych r.drop", fx))
    if (length(reverse) > 0 && missing == "listwise") {
      raw <- as.matrix(d[, items])
      ak <- suppressWarnings(suppressMessages(psych::alpha(as.data.frame(raw), keys = reverse, warnings = FALSE)))
      near(ak$total$raw_alpha, m$alpha, 1e-10, paste("psych keyed alpha", fx))
    }
  }
  mu <- if (missing == "pairwise") colMeans(X, na.rm = TRUE) else wmean(X, w)
  sdv <- sqrt(diag(C))
  if (missing == "pairwise") sdv <- apply(X, 2, sd, na.rm = TRUE)
  nn <- if (missing == "pairwise") min(pair_n(X)) else ns
  f <- if (length(items) >= 3) fa1(m$R, nn) else NULL
  rel_cases[[length(rel_cases) + 1]] <<- list(
    fixture = fx, items = strs(items), reverse = strs(reverse),
    scale_min = numx(smin), scale_max = numx(smax),
    missing = missing, weight = kind, n_rows = num(nrow(X)), n_star = num(nn),
    alpha = num(m$alpha), alpha_standardized = num(m$alpha_std),
    mean_inter_item_r = num(m$rbar),
    item_total_r = numv(m$r_drop), alpha_if_deleted = numv(m$alpha_if_deleted),
    item_mean = numv(mu), item_sd = numv(sdv),
    inter_item_correlation = numm(m$R),
    omega_total_model = if (is.null(f)) jnull else num(f$omega_total_model),
    omega_total_observed = if (is.null(f)) jnull else num(f$omega_total_observed),
    heywood = if (is.null(f)) FALSE else f$heywood
  )
  if (!is.null(f)) {
    fa_cases[[length(fa_cases) + 1]] <<- list(
      fixture = fx, fields = strs(items), reverse = strs(reverse),
      missing = missing, weight = kind, n_obs = num(nn),
      correlation = numm(m$R), smc_start = numv(f$smc),
      loadings = numv(f$loadings), uniquenesses = numv(f$uniquenesses),
      communalities = numv(f$communalities),
      sum_loadings = num(f$sum_loadings), sum_uniquenesses = num(f$sum_uniquenesses),
      sum_r = num(f$sum_r),
      omega_total_model = num(f$omega_total_model),
      omega_total_observed = num(f$omega_total_observed),
      heywood = f$heywood, psych_messages = f$warnings
    )
  }
}
for (kind in w_kinds) {
  add_rel("likert_ties", lk, paste0("q", 1:6), kind)
  add_rel("reversed_items", rv, paste0("r", 1:5), kind, reverse = c("r2", "r4"), smin = 1, smax = 5)
  add_rel("reversed_items", rv, paste0("r", 1:5), kind)
  add_rel("attitude", att, names(datasets::attitude), kind)
  add_rel("nulls", nl, paste0("x", 1:4), kind)
  add_rel("likert_ties", lk, c("q1", "q2"), kind)
}
add_rel("nulls", nl, paste0("x", 1:4), "none", "pairwise")
add_rel("heywood", hw, c("h1", "h2", "h3"), "none")
if (!fa_cases[[length(fa_cases)]]$heywood) stop("heywood fixture did not produce a Heywood case")

rel_doc <- c(mv_meta("reliability (alpha) and omega",
  "psych::alpha(check.keys = FALSE) (unweighted, asserted against the manual formulas); manual alpha on the weighted covariance; omega from psych::fa(nfactors = 1, fm = 'minres')",
  "Reversed items are x' = scale_min + scale_max - x BEFORE the fold (psych::alpha(keys=) asserted equal). item_total_r is the corrected item-total r (psych r.drop). item_sd is sqrt(diag(cov)) under the one rule (pairwise: per-item sd). omega_total_model = (sum lambda)^2 / ((sum lambda)^2 + sum psi); omega_total_observed = 1 - sum psi / sum(R) (psych::omega's omega.tot). Both are null for p = 2 (no one-factor fit). heywood flags any psi <= 0.005 or |lambda| >= 1 (see mv_fa_minres)."),
  list(cases = rel_cases))
fa_doc <- c(mv_meta("one-factor minres",
  "psych::fa(r = R, nfactors = 1, fm = 'minres', n.obs = n, rotate = 'none')",
  "Loadings signed so sum(lambda) > 0. smc_start is psych::smc(R), the documented start. uniquenesses are psych's 1 - communality, so a Heywood case shows psi <= 0 (ultra-Heywood: |lambda| > 1), with psych's own warning text in psych_messages; heywood = any psi <= 0.005 or any |lambda| >= 1."),
  list(cases = fa_cases))

# ------------------------------------------------------ PCA / KMO / Bartlett
pca_cases <- list()
add_pca <- function(fx, df, cols, kind, on = "correlation", missing = "listwise") {
  d <- if (missing == "listwise") listwise(df, cols) else df
  X <- as.matrix(d[, cols])
  w <- wvec(d, kind)
  ns <- n_star(w, kind)
  if (missing == "pairwise") {
    # Pairwise: R is cor(use = "pairwise") — each pair over its own rows,
    # the MAT_CORRELATION matrix Pulse analyses — NOT cov2cor of the
    # pairwise covariance (whose variances rest on other rows).
    S <- cov(X, use = "pairwise.complete.obs"); ns <- min(pair_n(X))
    R <- cor(X, use = "pairwise.complete.obs")
  } else {
    S <- wcov(X, w, kind)
    R <- cov2cor(S)
  }
  M <- if (on == "correlation") R else S
  e <- eigen(M, symmetric = TRUE)
  V <- sign_fix(e$vectors)
  lam <- e$values
  L <- sweep(V, 2, sqrt(pmax(lam, 0)), "*")
  k <- if (on == "correlation") sum(lam > 1) else NA_real_
  comm <- if (!is.na(k) && k > 0) rowSums(L[, seq_len(k), drop = FALSE]^2) else rep(NA_real_, ncol(M))
  if (kind == "none" && missing == "listwise") {
    pc <- prcomp(X, scale. = (on == "correlation"))
    near(pc$sdev^2, lam, 1e-9, paste("prcomp", fx))
  }
  kmo <- psych::KMO(R)
  bt <- psych::cortest.bartlett(R, n = ns)
  pca_cases[[length(pca_cases) + 1]] <<- list(
    fixture = fx, fields = strs(cols), on = on, missing = missing, weight = kind,
    n_rows = num(nrow(X)), n_star = num(ns), input = numm(M),
    eigenvalues = numv(lam), eigenvectors = numm(V), loadings = numm(L),
    explained_variance = numv(lam / sum(lam)), cumulative = numv(cumsum(lam) / sum(lam)),
    kaiser_components = if (is.na(k)) jnull else num(k),
    communalities_kaiser = numv(comm),
    kmo = num(kmo$MSA), kmo_msa = numv(kmo$MSAi),
    bartlett_chisq = num(bt$chisq), bartlett_df = num(bt$df), bartlett_p = num(bt$p.value)
  )
}
for (kind in w_kinds) {
  add_pca("attitude", att, names(datasets::attitude), kind)
  add_pca("attitude", att, names(datasets::attitude), kind, on = "covariance")
  add_pca("mtcars", mt, names(datasets::mtcars), kind)
  add_pca("mtcars", mt, c("mpg", "disp", "hp", "drat", "wt", "qsec"), kind, on = "covariance")
  add_pca("likert_ties", lk, paste0("q", 1:6), kind)
  add_pca("weighted", wd, paste0("x", 1:4), kind)
  add_pca("nulls", nl, paste0("x", 1:4), kind)
}
add_pca("nulls", nl, paste0("x", 1:4), "none", missing = "pairwise")
pca_doc <- c(mv_meta("principal components, KMO and Bartlett",
  "eigen(M, symmetric = TRUE) (prcomp asserted on unweighted listwise); psych::KMO(R); psych::cortest.bartlett(R, n = n_star)",
  "Eigenvectors carry Pulse's sign convention (largest-magnitude component positive, tie within 2^-26 -> lowest index); loadings = eigenvector * sqrt(lambda). Eigenvalues descending. kaiser_components / communalities_kaiser are for correlation input only (lambda > 1). KMO and Bartlett always run on the correlation; n_star = N listwise, min pair n pairwise, sum(w) frequency, Kish n_eff probability. The pairwise correlation is cor(use = 'pairwise.complete.obs') (each pair over its own rows), not cov2cor of the pairwise covariance."),
  list(cases = pca_cases))

# ------------------------------------------------------------ collinearity
col_cases <- list()
colldiag_parts <- function(cd) list(cond = as.numeric(cd$condindx), pi = unname(as.matrix(cd$pi)), names = colnames(cd$pi))
# Belsley from the moments, Pulse's construction: B = [[1, mu'], [mu,
# D R D + mu mu']] (D = population sds) for the uncentered variant, R
# itself for the centered one. colldiag runs on chol(B) — a pseudo data
# matrix with crossprod(chol(B)) == B — so its scaled SVD is exactly the
# scaled eigen-decomposition of B.
colldiag_moments <- function(M, names) {
  U <- chol(M); colnames(U) <- names
  colldiag_parts(perturb::colldiag(U, scale = TRUE, center = FALSE, add.intercept = FALSE))
}
uncentered_b <- function(R, mu, sdp) {
  B <- diag(sdp, length(sdp)) %*% R %*% diag(sdp, length(sdp)) + mu %o% mu
  rbind(c(1, mu), cbind(mu, B))
}
add_coll <- function(fx, df, response, preds, kind, missing = "listwise") {
  if (missing == "pairwise") {
    # Pairwise (unweighted): R = cor(use = "pairwise"); VIF = diag(R^-1)
    # (no lm to assert against — car::vif needs complete rows). Belsley
    # on Pulse's pairwise moments: each member's own-row mean and
    # population sd around R.
    X <- as.matrix(df[, preds])
    R <- cor(X, use = "pairwise.complete.obs")
    vif <- diag(solve(R))
    mu <- colMeans(X, na.rm = TRUE)
    sdp <- sqrt(colSums(sweep(X, 2, mu)^2, na.rm = TRUE) / colSums(!is.na(X)))
    un <- colldiag_moments(uncentered_b(R, mu, sdp), c("intercept", preds))
    ce <- colldiag_moments(R, preds)
    col_cases[[length(col_cases) + 1]] <<- list(
      fixture = fx, response = jnull, predictors = strs(preds), weight = kind, missing = missing,
      n_rows = num(nrow(X)), pair_n = numm(pair_n(X)),
      vif = numv(vif), tolerance = numv(1 / vif),
      uncentered = list(columns = strs(c("intercept", preds)), condition_indices = numv(un$cond), variance_decomposition = numm(un$pi)),
      centered = list(columns = strs(preds), condition_indices = numv(ce$cond), variance_decomposition = numm(ce$pi))
    )
    return(invisible())
  }
  d <- listwise(df, c(response, preds))
  X <- as.matrix(d[, preds])
  w <- wvec(d, kind)
  R <- wcor(X, w, kind)
  vif <- diag(solve(R))
  fml <- as.formula(paste(response, "~", paste(preds, collapse = " + ")))
  if (kind == "none") {
    near(car::vif(lm(fml, data = d)), vif, 1e-9, paste("car::vif", fx))
  } else {
    dd <- d; dd$.w <- w
    near(car::vif(lm(fml, data = dd, weights = .w)), vif, 1e-9, paste("weighted car::vif", fx))
  }
  sw <- sqrt(w)
  if (kind == "none") {
    un <- colldiag_parts(perturb::colldiag(X, scale = TRUE, center = FALSE, add.intercept = TRUE))
    ce <- colldiag_parts(perturb::colldiag(X, scale = TRUE, center = TRUE, add.intercept = FALSE))
    # The moment construction (the pairwise case's route) equals the
    # data route listwise.
    mu <- colMeans(X)
    sdp <- sqrt(colMeans(sweep(X, 2, mu)^2))
    um <- colldiag_moments(uncentered_b(R, mu, sdp), c("intercept", preds))
    cm <- colldiag_moments(R, preds)
    near(um$cond, un$cond, 1e-8, paste("moment colldiag uncentered", fx)); near(um$pi, un$pi, 1e-8, paste("moment pi uncentered", fx))
    near(cm$cond, ce$cond, 1e-8, paste("moment colldiag centered", fx)); near(cm$pi, ce$pi, 1e-8, paste("moment pi centered", fx))
  } else {
    Xi <- cbind(intercept = sw, X * sw)
    un <- colldiag_parts(perturb::colldiag(Xi, scale = TRUE, center = FALSE, add.intercept = FALSE))
    mu <- wmean(X, w)
    Xc <- sweep(X, 2, mu) * sw
    ce <- colldiag_parts(perturb::colldiag(Xc, scale = TRUE, center = FALSE, add.intercept = FALSE))
  }
  col_cases[[length(col_cases) + 1]] <<- list(
    fixture = fx, response = response, predictors = strs(preds), weight = kind, missing = missing,
    n_rows = num(nrow(X)),
    vif = numv(vif), tolerance = numv(1 / vif),
    uncentered = list(columns = strs(c("intercept", preds)), condition_indices = numv(un$cond), variance_decomposition = numm(un$pi)),
    centered = list(columns = strs(preds), condition_indices = numv(ce$cond), variance_decomposition = numm(ce$pi))
  )
}
for (kind in w_kinds) {
  add_coll("attitude", att, "rating", setdiff(names(datasets::attitude), "rating"), kind)
  add_coll("mtcars", mt, "mpg", c("disp", "hp", "drat", "wt", "qsec"), kind)
  add_coll("weighted", wd, "y", paste0("x", 1:4), kind)
}
add_coll("nulls", nl, NULL, paste0("x", 1:4), "none", "pairwise")
col_doc <- c(mv_meta("collinearity diagnostics",
  "car::vif(lm(response ~ predictors[, weights])) (asserted) == diag(solve(R_w)); perturb::colldiag(X, scale = TRUE, center = FALSE, add.intercept = TRUE) and (center = TRUE, add.intercept = FALSE)",
  "Weighted colldiag runs on sqrt(w)-scaled rows (uncentered with a sqrt(w) intercept column; centered on the weighted mean), equivalent to W(Sigma + mu mu^T) after column scaling. condition_indices are ascending as perturb returns them; variance_decomposition rows follow them, columns follow 'columns'. Response is used only for car::vif's lm and does not enter the diagnostics. The one pairwise case (unweighted, response null) is built from the moments as Pulse builds it: R = cor(use = 'pairwise.complete.obs'), VIF = diag(solve(R)) (no car::vif: lm needs complete rows); colldiag on chol(B), B = [[1, mu'], [mu, D R D + mu mu']] with each member's own-row mean and population sd (uncentered) and on chol(R) (centered) — the moment route is asserted equal to the data route on every unweighted listwise case."),
  list(cases = col_cases))

# ------------------------------------------------------------ vcov(lm/glm)
vc_cases <- list()
add_vcov <- function(fx, df, fml, family, kind) {
  vars <- all.vars(fml)
  d <- listwise(df, vars)
  w <- wvec(d, kind)
  ns <- n_star(w, kind)
  ws <- w * ns / sum(w)
  d$.ws <- ws
  if (family == "gaussian") {
    if (kind == "none") {
      fit <- lm(fml, data = d); V <- vcov(fit); b <- coef(fit)
    } else {
      Xm <- model.matrix(fml, d); y <- model.response(model.frame(fml, d))
      XtWX <- crossprod(Xm * ws, Xm)
      b <- drop(solve(XtWX, crossprod(Xm * ws, y)))
      e <- y - drop(Xm %*% b)
      s2 <- sum(ws * e^2) / (ns - ncol(Xm))
      V <- s2 * solve(XtWX)
      dimnames(V) <- list(colnames(Xm), colnames(Xm)); names(b) <- colnames(Xm)
      if (kind == "frequency") {
        fe <- lm(fml, data = expand(d, w))
        near(vcov(fe), V, 1e-9, paste("expanded lm vcov", fx)); near(coef(fe), b, 1e-9, "expanded lm coef")
      }
    }
  } else {
    fam <- switch(family, binomial = binomial(), poisson = poisson())
    # IRLS run to convergence well past glm's default epsilon (1e-8 on
    # the deviance), so the oracle is the MLE, not one stopping point.
    ctl <- glm.control(epsilon = 1e-14, maxit = 200)
    fit <- if (kind == "none") glm(fml, family = fam, data = d, control = ctl) else suppressWarnings(glm(fml, family = fam, data = d, weights = .ws, control = ctl))
    if (!fit$converged) stop("glm did not converge: ", fx, " ", family, " ", kind)
    V <- vcov(fit); b <- coef(fit)
    if (kind == "frequency") {
      fe <- glm(fml, family = fam, data = expand(d, w), control = ctl)
      near(vcov(fe), V, 1e-7, paste("expanded glm vcov", fx)); near(coef(fe), b, 1e-7, "expanded glm coef")
    }
  }
  vc_cases[[length(vc_cases) + 1]] <<- list(
    fixture = fx, formula = paste(deparse(fml), collapse = ""), family = family, weight = kind,
    n_rows = num(nrow(d)), n_star = num(ns),
    terms = strs(colnames(V)), coefficients = numv(b), std_errors = numv(sqrt(diag(V))),
    vcov = numm(V), correlation = numm(cov2cor(V))
  )
}
for (kind in w_kinds) {
  add_vcov("mtcars", mt, mpg ~ wt + hp + qsec, "gaussian", kind)
  add_vcov("mtcars", mt, am ~ wt + hp, "binomial", kind)
  add_vcov("mtcars", mt, carb ~ wt + hp, "poisson", kind)
  add_vcov("attitude", att, rating ~ complaints + privileges + learning + raises + critical + advance, "gaussian", kind)
  add_vcov("weighted", wd, y ~ x1 + x2 + x3 + x4, "gaussian", kind)
  add_vcov("weighted", wd, y_bin ~ x1 + x3, "binomial", kind)
  add_vcov("weighted", wd, y_count ~ x2 + x4, "poisson", kind)
}
vc_doc <- c(mv_meta("regression coefficient covariance",
  "vcov(lm(...)), vcov(glm(..., family = binomial | poisson)); weighted per the one rule",
  "Terms use R's '(Intercept)' spelling. gaussian weighted: the frequency formula on w* = w*N*/sum(w): V = s2 * (X'W*X)^-1 with s2 = sum(w* e^2)/(N* - p) (frequency asserted == lm on rep()-expanded rows). glm runs IRLS to glm.control(epsilon = 1e-14, maxit = 200). glm weighted: glm(weights = w*), dispersion fixed at 1, so the kind does not move V (frequency asserted == expanded glm). correlation = cov2cor(vcov); std_errors = sqrt(diag(vcov))."),
  list(cases = vc_cases))

# --------------------------------------------------------------- nearPD
npd_cases <- list()
Xn <- as.matrix(np)
Rp <- cor(Xn, use = "pairwise.complete.obs")
Sp <- cov(Xn, use = "pairwise.complete.obs")
ev <- eigen(Rp, symmetric = TRUE)$values
if (min(ev) >= 0) stop("nonpsd_pairwise fixture is PSD")
for (d2e in c(TRUE, FALSE)) {
  r <- Matrix::nearPD(Rp, corr = TRUE, do2eigen = d2e)
  M <- as.matrix(r$mat); dimnames(M) <- NULL
  Dg <- sqrt(diag(Sp))
  Sc <- M * outer(Dg, Dg)
  npd_cases[[length(npd_cases) + 1]] <- list(
    fixture = "nonpsd_pairwise", fields = strs(colnames(Xn)), missing = "pairwise",
    do2eigen = d2e, conv_tol = num(1e-7), eig_tol = num(1e-6), posd_tol = num(1e-8), maxit = num(100),
    pair_n = numm(pair_n(Xn)),
    input_correlation = numm(Rp), input_eigenvalues = numv(ev),
    nearest = numm(M), nearest_eigenvalues = numv(eigen(M, symmetric = TRUE, only.values = TRUE)$values),
    r_eigenvalues = numv(r$eigenvalues),
    frobenius_adjustment = num(norm(M - Rp, "F")),
    norm_f = num(r$normF), iterations = num(r$iterations), converged = r$converged,
    input_covariance = numm(Sp), repaired_covariance = numm(Sc)
  )
}
npd_doc <- c(mv_meta("nearest correlation matrix",
  "Matrix::nearPD(R, corr = TRUE, do2eigen = TRUE | FALSE) with the default tolerances",
  "Higham (2002) alternating projections with Dykstra's correction. do2eigen = TRUE adds a final posdefify step; the do2eigen = FALSE case is the bare Higham fixed point. repaired_covariance = D * nearest * D with D = sqrt(diag(pairwise covariance)) (a covariance input is repaired via its correlation and scaled back). frobenius_adjustment = ||nearest - input||_F. nearest_eigenvalues are eigen(nearest); r_eigenvalues is nearPD's own $eigenvalues, which under do2eigen = FALSE belong to the last iterate before the unit-diagonal projection, not to nearest."),
  list(cases = npd_cases))

write_golden("mv_fixtures", fixture_doc)
write_golden("mv_rank_correlation", rank_doc)
write_golden("mv_partial_correlation", pcor_doc)
write_golden("mv_reliability", rel_doc)
write_golden("mv_fa_minres", fa_doc)
write_golden("mv_pca", pca_doc)
write_golden("mv_collinearity", col_doc)
write_golden("mv_vcov", vc_doc)
write_golden("mv_near_pd", npd_doc)
