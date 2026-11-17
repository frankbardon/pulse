# gen_ordinal.R — R oracle for REG_ORDINAL, the proportional-odds
# (cumulative link) ordinal regression (U65 E4-S1, #630): MASS::polr's
# model — P(Y <= k | x) = F(zeta_k - x'beta), F the logistic (polr method
# "logistic", Pulse link logit), standard normal ("probit") or the
# complementary log-log ("cloglog", 1 - exp(-exp(q))) — and the Brant
# test of the proportional-odds assumption (the brant package).
#
# Writes <out_dir>/ordinal.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_ordinal.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite 2.0.0 + MASS 7.3-66 (polr) + sandwich 3.1-3
# (sandwich / vcovCL cross-checks) + brant 0.3.0 (installed from the
# Posit noble binary repo) + ordinal 2026.7-26 (clm, nominal_test; with
# ucminf 1.2.3, installed with the Homebrew Fortran workaround; versions
# recorded in the golden).
#
# What is the reference, and why it is polished:
#   * polr maximises the log-likelihood with optim's BFGS and reports the
#     Hessian optim() differentiates NUMERICALLY from the gradient, so its
#     coefficients sit a stopping distance (~1e-6) from the MLE and its
#     vcov carries finite-difference error (~1e-5). The golden is the MLE
#     itself: polr's estimate polished by Newton steps on the ANALYTIC
#     gradient and Hessian written out below, until the step is below
#     1e-15 relative; the vcov is the inverse of that analytic observed
#     information. The script stops unless polr (fitted with
#     reltol = 1e-14) agrees with the polished point to 1e-6 and with its
#     vcov to 1e-4, so the closed forms are polr's model.
#   * The Brant test re-runs brant::brant's algorithm (the J - 1 binary
#     logit glm fits of 1{y > m}, their cross-covariance
#     (X'W_m X)^-1 X'W_ml X (X'W_l X)^-1, W_ml = pi_l (1 - pi_m), the
#     Wald statistic of D beta* = 0 — omnibus df (J - 2) p, per term
#     J - 2) with the binary glms refitted to their fixed point
#     (epsilon 1e-14); brant's own glm stops at epsilon 1e-8, so the
#     script stops unless brant::brant agrees to 1e-6.
#   * Robust covariances: hc0 = H^-1 M H^-1, M = sum w s s' (s the
#     per-row score), hc1 = hc0 n/(n - k), cluster = H^-1 (sum_g S_g S_g')
#     H^-1 G/(G - 1) (n - 1)/(n - k), k = p + J - 1; cross-checked against
#     sandwich::sandwich(polr) / vcovCL(type = "HC1") to 1e-4 (they read
#     polr's numerical Hessian).
#   * Exponentiated (U68 E3-S2, exponentiate: true): on the predictors
#     only (the thresholds are not effects), the cumulative odds ratios
#     exp(beta) — exp(coef(polr)), asserted to 1e-6 — their delta-method
#     SE exp(beta) SE and the exponentiated Wald interval
#     exp(beta -/+ qnorm(0.975) SE), on the case's covariance (classical
#     or robust); a classical unweighted case also matches
#     exp(confint.default(polr)) to 1e-4 (polr's numerical vcov).
#   * Nominal test (U68 E6-S1, params.nominal_test, every link): the
#     likelihood-ratio test of proportional odds against the partial
#     proportional-odds model (ordinal::nominal_test, which refits clm
#     with nominal = ~term) — per predictor (that predictor's slope free
#     at every cumulative split, df J - 2) and an omnibus (every
#     predictor free, df (J - 2) p; clm(nominal = ~ all) against clm).
#     Each partial fit is polished by Newton on its ANALYTIC gradient
#     and Hessian (ord_ppo_parts) from the polished proportional-odds
#     MLE; the script stops unless clm's logLik agrees with the polished
#     one to 1e-8 and nominal_test's LRT with the polished statistic to
#     1e-6. A partial fit that does not converge (a split a predictor
#     separates) or whose fitted cumulative probabilities cross at a
#     fitted row is null (nominal_test$failed says which; clm reports a
#     convergence code other than 0 there and nominal_test still prints
#     a number, kept as r_reported).
#
# Weights follow REG_GLM's rule: frequency weights are polr(weights = f),
# asserted equal to the rep()-expanded fit (and the expanded Brant
# test); probability weights read w* = w n_eff / sum(w) (Kish n_eff), the
# frequency formula on w* — no two_log_lik / aic. Each classical case
# without params.levels also carries a holdout (U68 E1-S3): eight new
# grid rows [x..., y, w, g] (holdout_rows) and their proper scores at the
# polished MLE (holdout: n, log_loss, brier, accuracy, rps — ord_scores'
# closed forms over predict(polr, type = "probs")). Fitted probabilities
# are the weighted means over the fitted rows of P(Y = k | x_i); the row
# scores (row_eta = x'beta, row_probabilities = P(Y = k | x_i) per row, the
# matrix predict(polr, type = "probs") returns) are at the polished MLE,
# on the classical-covariance cases (a robust covariance moves no score).
#
# The `modifiers` block (U68 E2-S1) adds the resample / selection
# oracles — the manual-deletion jackknife and MASS::stepAIC on polr —
# drawn after every case under its own seed (see the block below).
#
# Seeded (set.seed(20261102)); draws its own rows, so no other golden
# moves. Rows are [x..., y, w, g] (y the 1-based level index, g the
# cluster id, w = 1 unweighted).

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(MASS))
suppressPackageStartupMessages(library(sandwich))
suppressPackageStartupMessages(library(brant))
suppressPackageStartupMessages(library(ordinal))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_ordinal.R pins R 4.6.1, got ", R.version.string)
ord_pins <- c(MASS = "7.3.66", sandwich = "3.1.3", brant = "0.3.0", jsonlite = "2.0.0",
              ordinal = "2026.7.26", ucminf = "1.2.3")
for (p in names(ord_pins)) {
  if (as.character(packageVersion(p)) != ord_pins[[p]]) stop("gen_ordinal.R pins ", p, " ", ord_pins[[p]], ", got ", packageVersion(p))
}

ord_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
# ord_g17_na is ord_g17 with NA written as null (a nominal-test figure
# whose partial fit failed); ord_num_na likewise.
ord_g17_na <- function(v) {
  v <- unname(as.numeric(v))
  if (any(is.infinite(v) | is.nan(v))) stop("non-finite reference value")
  structure(paste0("[", paste(ifelse(is.na(v), "null", sprintf("%.17g", v)), collapse = ","), "]"), class = "json")
}
ord_num_na <- function(x) if (is.na(x)) structure("null", class = "json") else ord_num(x)
ord_num <- function(x) {
  if (length(x) != 1 || !is.finite(x)) stop("ord_num takes a finite scalar")
  structure(sprintf("%.17g", x), class = "json")
}
ord_mat <- function(m) structure(paste0("[", paste(sapply(seq_len(nrow(m)), function(i) ord_g17(m[i, ])), collapse = ","), "]"), class = "json")
# ord_check: relative error, against max(|want|, floor) (floor 1 for the
# loose cross-checks of a statistic that may sit near 0).
ord_check <- function(what, got, want, tol, floor = 1e-12) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  err <- max(abs(got - want) / pmax(abs(want), floor))
  if (is.na(err) || err > tol) stop(what, ": ", err, " > ", tol)
}

# The links: polr's methods. F the cdf, f its density, fp f'.
ord_links <- list(
  logit = list(method = "logistic", F = plogis, f = dlogis,
               fp = function(x) dlogis(x) * (1 - 2 * plogis(x))),
  probit = list(method = "probit", F = pnorm, f = dnorm, fp = function(x) -x * dnorm(x)),
  cloglog = list(method = "cloglog", F = function(x) -expm1(-exp(x)), f = function(x) exp(x - exp(x)),
                 fp = function(x) exp(x - exp(x)) * (1 - exp(x)))
)

# ord_parts: the log-likelihood, per-row scores and the Hessian at
# theta = c(beta, zeta) for rows X (n x p), y in 1..J, weights w.
ord_parts <- function(theta, X, y, w, J, L) {
  p <- ncol(X)
  beta <- theta[seq_len(p)]
  zeta <- theta[p + seq_len(J - 1)]
  eta <- drop(X %*% beta)
  up <- y < J
  lo <- y > 1
  a <- ifelse(up, c(zeta, 0)[y] - eta, 0)
  b <- ifelse(lo, c(0, zeta)[y] - eta, 0)
  Fa <- ifelse(up, L$F(a), 1); Fb <- ifelse(lo, L$F(b), 0)
  fa <- ifelse(up, L$f(a), 0); fb <- ifelse(lo, L$f(b), 0)
  fpa <- ifelse(up, L$fp(a), 0); fpb <- ifelse(lo, L$fp(b), 0)
  pr <- Fa - Fb
  q <- p + J - 1
  S <- matrix(0, length(y), q)
  H <- matrix(0, q, q)
  g <- (fa - fb) / pr
  for (i in seq_along(y)) {
    s <- numeric(q)
    s[seq_len(p)] <- -g[i] * X[i, ]
    h <- matrix(0, q, q)
    h[seq_len(p), seq_len(p)] <- ((fpa[i] - fpb[i]) / pr[i] - g[i]^2) * tcrossprod(X[i, ])
    if (up[i]) {
      k <- p + y[i]
      s[k] <- fa[i] / pr[i]
      h[k, k] <- fpa[i] / pr[i] - (fa[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (-fpa[i] / pr[i] + fa[i] * g[i] / pr[i])
    }
    if (lo[i]) {
      k <- p + y[i] - 1
      s[k] <- -fb[i] / pr[i]
      h[k, k] <- -fpb[i] / pr[i] - (fb[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (fpb[i] / pr[i] - fb[i] * g[i] / pr[i])
    }
    if (up[i] && lo[i]) {
      ku <- p + y[i]; kl <- ku - 1
      h[ku, kl] <- h[kl, ku] <- fa[i] * fb[i] / pr[i]^2
    }
    S[i, ] <- s
    H <- H + w[i] * h
  }
  P <- sapply(seq_len(J), function(k) {
    u <- if (k < J) L$F(zeta[k] - eta) else 1
    l <- if (k > 1) L$F(zeta[k - 1] - eta) else 0
    u - l
  })
  list(ll = sum(w * log(pr)), S = S, grad = colSums(w * S), H = H, P = P)
}

# ord_scores: the proper scores of a holdout (U68 E1-S3) — rows' level
# probabilities P (n x K, predict(polr, type = "probs")) against their
# levels y (1..K), weighted means with weights w: log_loss = -ln P[y]
# (natural log), brier = sum_k (P_k - [y = k])^2, accuracy = [first
# argmax = y], and (ordered) rps = sum_{k<K} (F_k - [y <= k])^2 / (K - 1),
# F_k the cumulative probability.
ord_scores <- function(P, y, w, ordered = TRUE) {
  n <- nrow(P); K <- ncol(P)
  O <- outer(y, seq_len(K), "==") * 1
  sw <- sum(w)
  out <- list(n = n,
    log_loss = ord_num(sum(w * -log(P[cbind(seq_len(n), y)])) / sw),
    brier = ord_num(sum(w * rowSums((P - O)^2)) / sw),
    accuracy = ord_num(sum(w * (max.col(P, ties.method = "first") == y)) / sw))
  if (ordered) {
    Fc <- t(apply(P, 1, cumsum))[, seq_len(K - 1), drop = FALSE]
    Oc <- outer(y, seq_len(K - 1), "<=") * 1
    out$rps <- ord_num(sum(w * rowSums((Fc - Oc)^2) / (K - 1)) / sw)
  }
  out
}

# ord_newton polishes theta to the MLE.
ord_newton <- function(theta, X, y, w, J, L) {
  for (it in 1:100) {
    pt <- ord_parts(theta, X, y, w, J, L)
    step <- solve(pt$H, pt$grad)
    theta <- theta - step
    if (max(abs(step) / pmax(abs(theta), 1)) < 1e-15) break
  }
  if (it == 100) stop("Newton polish did not converge")
  theta
}

# ord_glm is a binary logit glm refitted to its fixed point.
ord_glm <- function(z, X, w, tight = TRUE) {
  d <- data.frame(z = z, X)
  f <- as.formula(paste("z ~", paste(colnames(X), collapse = " + ")))
  if (!tight) return(glm(f, family = "binomial", data = d))
  ctl <- glm.control(epsilon = 1e-14, maxit = 200)
  m <- suppressWarnings(glm(f, family = binomial(), data = d, weights = w, control = ctl))
  for (k in 1:50) {
    b <- coef(m)
    m <- suppressWarnings(glm(f, family = binomial(), data = d, weights = w, control = ctl, start = b))
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-15) break
  }
  if (!m$converged) stop("binary glm did not converge")
  m
}

# ord_brant is brant::brant's statistic with tight binary fits and prior
# weights w (1 unweighted): list(omnibus, per-term) of (X2, df, p). The
# cross-covariance block of (beta_l, beta_m), l > m, is the transpose of
# the (m, l) block (Brant 1990: Cov(b_l, b_m) = Cov(b_m, b_l)'), so V is
# symmetric. brant 0.3.0 stores the (m, l) block itself there, untransposed
# (asym = TRUE reproduces it): that moves only the omnibus statistic
# (per-term blocks are scalars per (m, l) pair), and the script checks
# brant::brant against the asym form.
ord_brant <- function(X, y, w, J, asym = FALSE, tight = TRUE) {
  K <- ncol(X)
  Xi <- cbind(1, X)
  fits <- lapply(seq_len(J - 1), function(m) ord_glm(as.numeric(y > m), X, w, tight))
  pis <- sapply(fits, fitted)
  bstar <- unlist(lapply(fits, function(m) coef(m)[-1]))
  V <- matrix(0, (J - 1) * K, (J - 1) * K)
  blk <- function(m) ((m - 1) * K + 1):(m * K)
  for (m in seq_len(J - 1)) V[blk(m), blk(m)] <- vcov(fits[[m]])[-1, -1]
  for (m in seq_len(J - 2)) for (l in (m + 1):(J - 1)) {
    Wm <- w * (pis[, m] - pis[, m]^2)
    Wl <- w * (pis[, l] - pis[, l]^2)
    Wml <- w * (pis[, l] - pis[, m] * pis[, l])
    c <- (solve(crossprod(Xi, Wm * Xi)) %*% crossprod(Xi, Wml * Xi) %*% solve(crossprod(Xi, Wl * Xi)))[-1, -1, drop = FALSE]
    V[blk(m), blk(l)] <- c
    V[blk(l), blk(m)] <- if (asym) c else t(c)
  }
  D <- matrix(0, (J - 2) * K, (J - 1) * K)
  for (i in seq_len(J - 2)) {
    D[blk(i), blk(1)] <- diag(K)
    D[blk(i), blk(i + 1)] <- -diag(K)
  }
  wald <- function(Dm, b, Vm) drop(t(Dm %*% b) %*% solve(Dm %*% Vm %*% t(Dm)) %*% (Dm %*% b))
  omni <- wald(D, bstar, V)
  terms <- sapply(seq_len(K), function(k) {
    s <- seq(k, K * (J - 1), by = K)
    Ds <- D[, s, drop = FALSE]
    Ds <- Ds[rowSums(Ds != 0) > 0, , drop = FALSE]
    wald(Ds, bstar[s], V[s, s, drop = FALSE])
  })
  list(omnibus = c(omni, (J - 2) * K, pchisq(omni, (J - 2) * K, lower.tail = FALSE)),
       terms = terms, terms_df = J - 2, terms_p = pchisq(terms, J - 2, lower.tail = FALSE))
}

# The partial proportional-odds model (nominal test): eta_ik = zeta_k -
# sum_j x_ij beta_jk, beta_jk = beta_j for a parallel predictor. theta
# holds per predictor one slope (parallel) or J - 1 split slopes
# (nominal), then the J - 1 thresholds; idx[j, k] is predictor j's
# slope at split k.
ord_ppo_index <- function(nom, J) {
  p <- length(nom); idx <- matrix(0L, p, J - 1); at <- 0L
  for (j in seq_len(p)) {
    if (nom[j]) { idx[j, ] <- at + seq_len(J - 1); at <- at + J - 1L } else { at <- at + 1L; idx[j, ] <- at }
  }
  list(idx = idx, zeta = at + seq_len(J - 1), q = at + J - 1L)
}
# ord_ppo_dir is d eta_ik / d theta.
ord_ppo_dir <- function(ix, x, k) {
  v <- numeric(ix$q); v[ix$zeta[k]] <- 1
  for (j in seq_along(x)) v[ix$idx[j, k]] <- v[ix$idx[j, k]] - x[j]
  v
}
# ord_ppo_parts: loglik, gradient and Hessian (A, B the upper / lower
# eta's gradients): s = (f(a) A - f(b) B) / p, H = sum w ((f'(a) A A' -
# f'(b) B B') / p - s s').
ord_ppo_parts <- function(theta, X, y, w, J, L, nom) {
  ix <- ord_ppo_index(nom, J)
  q <- ix$q; ll <- 0; grad <- numeric(q); H <- matrix(0, q, q)
  for (i in seq_along(y)) {
    up <- y[i] < J; lo <- y[i] > 1
    A <- if (up) ord_ppo_dir(ix, X[i, ], y[i]) else numeric(q)
    B <- if (lo) ord_ppo_dir(ix, X[i, ], y[i] - 1) else numeric(q)
    a <- sum(A * theta); b <- sum(B * theta)
    Fa <- if (up) L$F(a) else 1; Fb <- if (lo) L$F(b) else 0
    fa <- if (up) L$f(a) else 0; fb <- if (lo) L$f(b) else 0
    fpa <- if (up) L$fp(a) else 0; fpb <- if (lo) L$fp(b) else 0
    pr <- Fa - Fb
    s <- (fa * A - fb * B) / pr
    ll <- ll + w[i] * log(pr)
    grad <- grad + w[i] * s
    H <- H + w[i] * ((fpa * tcrossprod(A) - fpb * tcrossprod(B)) / pr - tcrossprod(s))
  }
  list(ll = ll, grad = grad, H = H)
}
# ord_ppo_fit polishes the partial model for nominal set nom from the
# proportional-odds MLE theta (beta, zeta): list(ll, failed) — failed
# "" or why the figure is null ("no_converge" / "crossing").
ord_ppo_fit <- function(theta, X, y, w, J, L, nom) {
  ix <- ord_ppo_index(nom, J)
  p <- ncol(X)
  th <- numeric(ix$q)
  for (j in seq_len(p)) th[ix$idx[j, ]] <- theta[j]
  th[ix$zeta] <- theta[p + seq_len(J - 1)]
  ok <- FALSE
  for (it in 1:100) {
    pt <- tryCatch(suppressWarnings(ord_ppo_parts(th, X, y, w, J, L, nom)), error = function(e) NULL)
    if (is.null(pt) || !is.finite(pt$ll)) break
    step <- tryCatch(solve(pt$H, pt$grad), error = function(e) NULL)
    if (is.null(step) || any(!is.finite(step))) break
    th <- th - step
    if (max(abs(step) / pmax(abs(th), 1)) < 1e-14) { ok <- TRUE; break }
  }
  if (!ok) return(list(ll = NA, failed = "no_converge"))
  pt <- ord_ppo_parts(th, X, y, w, J, L, nom)
  if (max(abs(pt$grad)) > 1e-9) stop("partial proportional-odds fit: gradient not zero at the polished point")
  eta <- sapply(seq_len(J - 1), function(k) apply(X, 1, function(x) sum(ord_ppo_dir(ix, x, k) * th)))
  if (J > 2 && any(eta[, -1, drop = FALSE] <= eta[, -(J - 1), drop = FALSE])) return(list(ll = NA, failed = "crossing"))
  list(ll = pt$ll, failed = "")
}
# ord_nominal is the nominal test of a polished proportional-odds fit
# theta (log-likelihood ll): per-predictor and omnibus LR statistics,
# cross-checked against ordinal::nominal_test / clm on the converged
# figures. dd carries yf, the predictors and .ws.
ord_nominal <- function(name, theta, ll, X, y, w, J, L, link, dd, preds) {
  p <- ncol(X)
  one <- function(nom) {
    r <- ord_ppo_fit(theta, X, y, w, J, L, nom)
    st <- 2 * (r$ll - ll)
    df <- (J - 2) * sum(nom)
    list(stat = st, df = df, p = if (is.na(st)) NA else pchisq(st, df, lower.tail = FALSE), failed = r$failed)
  }
  terms <- lapply(seq_len(p), function(j) { nom <- rep(FALSE, p); nom[j] <- TRUE; one(nom) })
  omni <- one(rep(TRUE, p))
  f <- as.formula(paste("yf ~", paste(preds, collapse = " + ")))
  # do.call puts the data and link in the call itself: nominal_test
  # refits through update(), which evaluates the call in its own frame.
  m0 <- do.call(clm, list(formula = f, data = dd, weights = quote(.ws), link = link))
  ord_check(paste(name, "clm logLik vs polished"), as.numeric(logLik(m0)), ll, 1e-8)
  nt <- nominal_test(m0)
  failed <- list()
  for (j in seq_len(p)) {
    if (terms[[j]]$failed == "") {
      ord_check(paste(name, "nominal_test LRT", preds[j]), nt[preds[j], "LRT"], terms[[j]]$stat, 1e-6, floor = 1)
      if (nt[preds[j], "Df"] != terms[[j]]$df) stop(name, ": nominal_test df")
    } else {
      failed[[length(failed) + 1]] <- list(scope = "term", term = preds[j], reason = terms[[j]]$failed,
                                           r_reported = ord_num_na(nt[preds[j], "LRT"]))
    }
  }
  m1 <- suppressWarnings(clm(f, nominal = as.formula(paste("~", paste(preds, collapse = " + "))), data = dd,
                             weights = .ws, link = link, convergence = "silent"))
  if (omni$failed == "") {
    if (m1$convergence$code != 0) stop(name, ": clm(nominal = ~ all) did not converge where the polished fit did")
    ord_check(paste(name, "omnibus clm LRT"), 2 * (as.numeric(logLik(m1)) - as.numeric(logLik(m0))), omni$stat, 1e-6, floor = 1)
  } else {
    failed[[length(failed) + 1]] <- list(scope = "omnibus", reason = omni$failed,
                                         r_reported = ord_num_na(2 * (as.numeric(logLik(m1)) - as.numeric(logLik(m0)))))
  }
  list(statistic = ord_num_na(omni$stat), df = omni$df, p_value = ord_num_na(omni$p),
       terms = ord_g17_na(sapply(terms, `[[`, "stat")), terms_df = J - 2,
       terms_p = ord_g17_na(sapply(terms, `[[`, "p")), failed = failed)
}

ord_cases <- list()

# ord_case fits one case: d has the predictor columns, y (1..J) and w;
# kind "" | "frequency" | "probability"; cov "classical" | "hc0" | "hc1"
# | "cluster" (on d$g); brant TRUE runs the test (logit only).
ord_case <- function(name, d, preds, link = "logit", kind = "", cov = "classical", brant_test = FALSE,
                     labels = NULL, levels_param = NULL, nominal_test = FALSE) {
  L <- ord_links[[link]]
  if (kind == "") d$w <- 1
  J <- max(d$y)
  if (!all(sort(unique(d$y)) == seq_len(J))) stop(name, ": empty level")
  X <- as.matrix(d[, preds, drop = FALSE])
  n <- nrow(d)
  w <- d$w
  nstar <- if (kind == "probability") sum(w)^2 / sum(w^2) else sum(w)
  ws <- if (kind == "probability") w * nstar / sum(w) else w
  yf <- factor(d$y, levels = seq_len(J))
  f <- as.formula(paste("yf ~", paste(preds, collapse = " + ")))
  dd <- data.frame(yf = yf, d[, preds, drop = FALSE], .ws = ws)
  pf <- suppressWarnings(polr(f, data = dd, weights = .ws, method = L$method, Hess = TRUE, control = list(reltol = 1e-14, maxit = 5000)))
  theta <- ord_newton(c(coef(pf), pf$zeta), X, d$y, ws, J, L)
  ord_check(paste(name, "polr vs polished MLE"), c(coef(pf), pf$zeta), theta, 1e-6)
  pt <- ord_parts(theta, X, d$y, ws, J, L)
  if (max(abs(pt$grad)) > 1e-9) stop(name, ": gradient not zero at the polished point")
  Hinv <- solve(-pt$H)
  ord_check(paste(name, "polr vcov vs analytic"), vcov(pf), Hinv, 1e-4)
  q <- length(theta)
  V <- Hinv
  if (cov != "classical") {
    if (cov == "cluster") {
      Sg <- rowsum(ws * pt$S, d$g)
      G <- nrow(Sg)
      V <- Hinv %*% crossprod(Sg) %*% Hinv * G / (G - 1) * (nstar - 1) / (nstar - q)
      if (kind == "") ord_check(paste(name, "vcovCL"), vcovCL(pf, cluster = d$g, type = "HC1"), V, 1e-4)
    } else {
      V <- Hinv %*% crossprod(sqrt(ws) * pt$S) %*% Hinv
      if (cov == "hc1") V <- V * nstar / (nstar - q)
      if (kind == "") ord_check(paste(name, "sandwich"), sandwich(pf, adjust = cov == "hc1"), V, 1e-4)
    }
  }
  se <- sqrt(diag(V))
  ll <- pt$ll
  Wk <- sapply(seq_len(J), function(k) sum(ws[d$y == k]))
  ll0 <- sum(Wk * log(Wk / sum(ws)))
  fp <- colSums(ws * pt$P) / sum(ws)
  doc <- list(name = name, link = link, weight = kind, covariance = cov, predictors = preds,
              levels = if (is.null(labels)) as.character(seq_len(J)) else labels,
              n_obs = n, coefficients = ord_g17(theta), std_errors = ord_g17(se), vcov = ord_mat(V),
              deviance = ord_num(-2 * ll), null_deviance = ord_num(-2 * ll0), fitted_probabilities = ord_g17(fp))
  if (!is.null(levels_param)) doc$levels_param <- levels_param
  pb <- seq_along(preds)
  zq <- qnorm(0.975)
  ord_check(paste(name, "exp(coef(polr))"), exp(coef(pf)), exp(theta[pb]), 1e-6)
  doc$exponentiated <- list(estimates = ord_g17(exp(theta[pb])), std_errors = ord_g17(exp(theta[pb]) * se[pb]),
                            ci_lower = ord_g17(exp(theta[pb] - zq * se[pb])), ci_upper = ord_g17(exp(theta[pb] + zq * se[pb])))
  if (cov == "classical" && kind == "") {
    ord_check(paste(name, "exp(confint.default(polr))"), exp(confint.default(pf)),
              cbind(exp(theta[pb] - zq * se[pb]), exp(theta[pb] + zq * se[pb])), 1e-4)
  }
  # Row scores (ATTR_REG_PREDICT, U68 #650): eta = x'beta and P(Y = k | x)
  # per fitted row at the polished MLE — predict(polr, type = "probs") and
  # polr's $lp, which read polr's own optimum, agree to 1e-6. A robust
  # covariance moves no score, so only the classical cases carry them.
  if (cov == "classical") {
    eta <- drop(X %*% theta[seq_len(ncol(X))])
    ord_check(paste(name, "polr lp vs polished eta"), pf$lp, eta, 1e-6, floor = 1)
    probs <- predict(pf, newdata = dd, type = "probs")
    if (is.null(dim(probs))) stop(name, ": predict(type = probs) is not a matrix")
    ord_check(paste(name, "predict(polr, type = probs) vs polished"), probs, pt$P, 1e-6, floor = 1)
    doc$row_eta <- ord_g17(eta)
    doc$row_probabilities <- ord_mat(pt$P)
    # Holdout (U68 E1-S3): eight new rows, a fixed grid (no RNG draw, so
    # no case's data move) cycling the levels, scored at the polished
    # MLE (predict(polr, newdata, type = "probs") agreeing to 1e-6) —
    # the rows a `holdout` predicate holds out of the same fit.
    if (is.null(levels_param)) {
      nh <- 8
      Xh <- matrix(sapply(seq_along(preds), function(j) ((seq_len(nh) * (j + 2)) %% 7 - 3) / 2), nrow = nh)
      colnames(Xh) <- preds
      yh <- (seq_len(nh) - 1) %% J + 1
      wh <- if (kind == "frequency") (seq_len(nh) - 1) %% 3 + 1 else if (kind == "probability") 0.25 * (seq_len(nh) %% 5 + 2) else rep(1, nh)
      Ph <- ord_parts(theta, Xh, yh, wh, J, L)$P
      ord_check(paste(name, "holdout predict(polr, type = probs) vs polished"),
        predict(pf, newdata = as.data.frame(Xh), type = "probs"), Ph, 1e-6, floor = 1)
      doc$holdout_rows <- lapply(seq_len(nh), function(i) ord_g17(c(Xh[i, ], yh[i], wh[i], 1)))
      doc$holdout <- ord_scores(Ph, yh, wh)
    }
  }
  if (kind != "probability") {
    doc$two_log_lik <- ord_num(2 * ll)
    doc$aic <- ord_num(-2 * ll + 2 * q)
    ord_check(paste(name, "AIC"), AIC(pf), -2 * ll + 2 * q, 1e-8)
  }
  if (kind == "frequency") {
    ex <- d[rep(seq_len(n), d$w), ]
    ex$w <- 1
    Xe <- as.matrix(ex[, preds, drop = FALSE])
    te <- ord_newton(theta, Xe, ex$y, ex$w, J, L)
    ord_check(paste(name, "weighted vs expanded coefficients"), te, theta, 1e-10)
    pe <- ord_parts(te, Xe, ex$y, ex$w, J, L)
    ord_check(paste(name, "weighted vs expanded vcov"), solve(-pe$H), Hinv, 1e-9)
    ord_check(paste(name, "weighted vs expanded 2ll"), 2 * pe$ll, 2 * ll, 1e-12)
  }
  if (brant_test) {
    br <- ord_brant(X, d$y, ws, J)
    doc$brant <- list(statistic = ord_num(br$omnibus[1]), df = br$omnibus[2], p_value = ord_num(br$omnibus[3]),
                      terms = ord_g17(br$terms), terms_df = br$terms_df, terms_p = ord_g17(br$terms_p))
    if (kind == "") {
      bb <- NULL
      invisible(capture.output(bb <- brant::brant(pf)))
      # brant::brant itself: its asymmetric fill and glm's default
      # stop (epsilon 1e-8) reproduce it to rounding. At the fixed
      # point the statistics move by up to ~3e-4 (that stop); the
      # per-term statistics do not read the asymmetric blocks.
      ba <- ord_brant(X, d$y, ws, J, asym = TRUE, tight = FALSE)
      ord_check(paste(name, "brant::brant X2"), bb[, 1], c(ba$omnibus[1], ba$terms), 1e-12, floor = 1)
      ord_check(paste(name, "brant::brant df"), bb[, 2], c(br$omnibus[2], rep(J - 2, length(preds))), 0)
      bt <- ord_brant(X, d$y, ws, J, asym = TRUE)
      ord_check(paste(name, "per-term X2, asymmetric vs symmetric fill"), bt$terms, br$terms, 1e-12, floor = 1)
    }
    if (kind == "frequency") {
      ex <- d[rep(seq_len(n), d$w), ]
      be <- ord_brant(as.matrix(ex[, preds, drop = FALSE]), ex$y, rep(1, nrow(ex)), J)
      ord_check(paste(name, "brant weighted vs expanded"), c(be$omnibus[1], be$terms), c(br$omnibus[1], br$terms), 1e-9)
    }
  }
  if (nominal_test) {
    doc$nominal_test <- ord_nominal(name, theta, ll, X, d$y, ws, J, L, link, dd, preds)
    if (kind == "frequency") {
      ex <- d[rep(seq_len(n), d$w), ]
      Xe <- as.matrix(ex[, preds, drop = FALSE])
      for (j in seq_along(preds)) {
        nom <- seq_along(preds) == j
        a <- ord_ppo_fit(theta, X, d$y, ws, J, L, nom)
        b <- ord_ppo_fit(theta, Xe, ex$y, rep(1, nrow(ex)), J, L, nom)
        if (a$failed != b$failed) stop(name, ": nominal weighted vs expanded verdict for ", preds[j])
        if (a$failed == "") ord_check(paste(name, "nominal weighted vs expanded", preds[j]), b$ll, a$ll, 1e-12)
      }
    }
  }
  doc$rows <- lapply(seq_len(n), function(i) ord_g17(c(unlist(d[i, preds]), d$y[i], d$w[i], d$g[i])))
  ord_cases[[length(ord_cases) + 1]] <<- doc
}

set.seed(20261102)
# ord_draw draws n rows of p predictors and a J-level response from the
# latent y* = x'beta + e, e from the link's distribution.
ord_draw <- function(n, beta, cuts, link = "logit") {
  p <- length(beta)
  X <- sapply(seq_len(p), function(j) round(if (j %% 2 == 1) rnorm(n) else runif(n, -1, 2), 4))
  colnames(X) <- paste0("x", seq_len(p))
  # P(e <= t) is the link's F: logistic, normal, 1 - exp(-exp(t)).
  e <- switch(link, logit = rlogis(n), probit = rnorm(n), cloglog = log(rexp(n)))
  ys <- drop(X %*% beta) + e
  y <- findInterval(ys, cuts) + 1
  d <- as.data.frame(X)
  d$y <- y
  d$w <- 1
  d$g <- sample(1:12, n, replace = TRUE)
  d
}

d <- ord_draw(220, c(0.9, -0.6), c(-0.5, 0.8))
ord_case("logit_two", d, c("x1", "x2"), brant_test = TRUE, nominal_test = TRUE)
ord_case("logit_two_hc0", d, c("x1", "x2"), cov = "hc0")
ord_case("logit_two_hc1", d, c("x1", "x2"), cov = "hc1")
ord_case("logit_two_cluster", d, c("x1", "x2"), cov = "cluster")

d <- ord_draw(260, c(0.7, -0.4, 0.5), c(-1, 0, 1.1))
ord_case("logit_four_labeled", d, c("x1", "x2", "x3"), brant_test = TRUE,
         labels = c("low", "mid", "high", "top"))

d <- ord_draw(240, c(0.6, -0.5, 0.3), c(-0.8, 0.1, 0.9), link = "probit")
ord_case("probit_three", d, c("x1", "x2", "x3"), link = "probit", nominal_test = TRUE)

d <- ord_draw(250, c(0.5, -0.7), c(-1.2, -0.3, 0.4, 1.3), link = "cloglog")
ord_case("cloglog_five", d, c("x1", "x2"), link = "cloglog", nominal_test = TRUE)

d <- ord_draw(90, c(0.8, -0.5), c(-0.4, 0.7))
d$w <- sample(1:4, 90, replace = TRUE)
ord_case("logit_freq", d, c("x1", "x2"), kind = "frequency", brant_test = TRUE, nominal_test = TRUE)
ord_case("logit_freq_hc1", d, c("x1", "x2"), kind = "frequency", cov = "hc1")

d <- ord_draw(150, c(0.8, -0.5), c(-0.4, 0.7))
d$w <- round(runif(150, 0.2, 3), 3)
ord_case("logit_prob", d, c("x1", "x2"), kind = "probability", brant_test = TRUE, nominal_test = TRUE)

# levels_subset: the response carries a fifth value 0 that params.levels
# leaves out — R's factor(y, levels = 1:J) makes those rows NA and polr
# drops them; the golden's rows are the kept ones, and the extra rows
# are added back by the Go test.
d <- ord_draw(200, c(0.6, 0.4), c(-0.6, 0.5))
ord_case("levels_subset", d, c("x1", "x2"), levels_param = c("1", "2", "3"))

# Nominal test (U68 E6-S1): weighted probit and cloglog cases, and a
# pinned non-convergent one — x3 is 0 on every lowest-level row, so the
# partial fit freeing x3 sends its first split's slope to infinity
# (clm reports convergence code 1; the figure is null) while the
# proportional-odds fit and the x1 / x2 tests stand. Drawn after every
# case above, so none of their rows move.
d <- ord_draw(180, c(0.7, -0.5), c(-0.6, 0.4, 1.2), link = "probit")
d$w <- sample(1:3, 180, replace = TRUE)
ord_case("probit_freq_nominal", d, c("x1", "x2"), link = "probit", kind = "frequency", nominal_test = TRUE)

d <- ord_draw(200, c(0.6, -0.6), c(-0.9, 0.2), link = "cloglog")
d$w <- round(runif(200, 0.3, 2.5), 3)
ord_case("cloglog_prob_nominal", d, c("x1", "x2"), link = "cloglog", kind = "probability", nominal_test = TRUE)

d <- ord_draw(120, c(0.9, -0.4), c(-0.5, 0.8))
d$x3 <- ifelse(d$y == 1, 0, rbinom(120, 1, 0.5))
ord_case("nominal_noconverge", d, c("x1", "x2", "x3"), nominal_test = TRUE)

# Resample and selection (U68 E2-S1, #651): REG_ORDINAL refit by the
# engine's modifier drivers. Drawn AFTER every case above under its own
# seed, so no case's rows move.
#   * jackknife: the manual-deletion jackknife on polr — n leave-one-out
#     refits (each polished to its MLE from the full fit and checked
#     against polr on the same deletion to 1e-6), SE = sqrt((n - 1)/n
#     sum_i (theta_(i) - mean)^2) over every coefficient (predictors and
#     thresholds); jackknife_by g: one refit per deleted cluster (sorted
#     values), (G - 1)/G.
#   * selection: MASS::stepAIC on polr — forward and both from yf ~ 1,
#     backward from the full model, scope the five predictors, k = 2 (AIC)
#     and log(n) (BIC); the criterion is extractAIC's -2 loglik + k * edf,
#     edf = predictors + thresholds. A greedy search over the POLISHED
#     deviances re-runs each path and must pick the same terms, every
#     step's winner ahead of the runner-up by more than 1e-3 (so polr's
#     ~1e-6 optimum cannot flip a step); selected lists the terms in
#     stepAIC's order (forward: the order they entered), coefficients the
#     final model's polished MLE (the selected terms, then thresholds).
set.seed(20261128)
ord_mdraw <- function(n, beta, cuts) {
  d <- ord_draw(n, beta, cuts)
  d$g <- sample(1:8, n, replace = TRUE)
  d
}
ord_fit_theta <- function(d, preds, J, start = NULL) {
  X <- as.matrix(d[, preds, drop = FALSE])
  yf <- factor(d$y, levels = seq_len(J))
  dd <- data.frame(yf = yf, d[, preds, drop = FALSE])
  f <- if (length(preds) == 0) yf ~ 1 else as.formula(paste("yf ~", paste(preds, collapse = " + ")))
  pf <- suppressWarnings(polr(f, data = dd, Hess = TRUE, control = list(reltol = 1e-14, maxit = 5000)))
  th <- ord_newton(if (is.null(start)) c(coef(pf), pf$zeta) else start, X, d$y, rep(1, nrow(d)), J, ord_links$logit)
  ord_check("modifier refit polr vs polished", c(coef(pf), pf$zeta), th, 1e-6, floor = 1)
  list(theta = th, dev = -2 * ord_parts(th, X, d$y, rep(1, nrow(d)), J, ord_links$logit)$ll)
}
ord_jk_se <- function(reps, scale) {
  m <- colMeans(reps)
  sqrt(scale * colSums(sweep(reps, 2, m)^2))
}
ord_rows <- function(d, preds) lapply(seq_len(nrow(d)), function(i) ord_g17(c(unlist(d[i, preds]), d$y[i], 1, d$g[i])))

d <- ord_mdraw(60, c(0.9, -0.7), c(-0.4, 0.6))
J <- max(d$y)
if (!all(sort(unique(d$y)) == seq_len(J))) stop("jackknife: empty level")
preds <- c("x1", "x2")
full <- ord_fit_theta(d, preds, J)
loo <- t(sapply(seq_len(nrow(d)), function(i) ord_fit_theta(d[-i, ], preds, J, full$theta)$theta))
gs <- sort(unique(d$g))
grp <- t(sapply(gs, function(g) ord_fit_theta(d[d$g != g, ], preds, J, full$theta)$theta))
ord_jackknife <- list(name = "jackknife_logit", predictors = preds, levels = as.character(seq_len(J)),
  coefficients = ord_g17(full$theta),
  std_errors = ord_g17(ord_jk_se(loo, (nrow(d) - 1) / nrow(d))),
  by_groups = length(gs),
  by_std_errors = ord_g17(ord_jk_se(grp, (length(gs) - 1) / length(gs))),
  rows = ord_rows(d, preds))

d <- ord_mdraw(160, c(1.0, -0.8, 0.22, 0, 0), c(-0.5, 0.8))
J <- max(d$y)
if (!all(sort(unique(d$y)) == seq_len(J))) stop("selection: empty level")
preds <- paste0("x", 1:5)
d$yf <- factor(d$y, levels = seq_len(J))
dsel <- d
# ord_greedy re-runs one stepAIC path over polished deviances; it
# returns the terms in entry order, stopping when no move lowers the
# criterion by more than 0 and refusing a step whose winner leads the
# runner-up (or the stay) by less than 1e-3.
ord_greedy <- function(direction, k) {
  crit <- function(active) ord_fit_theta(dsel, active, J)$dev + k * (length(active) + J - 1)
  active <- if (direction == "backward") preds else character(0)
  cur <- crit(active)
  repeat {
    moves <- list()
    if (direction != "backward") for (t in setdiff(preds, active)) moves[[length(moves) + 1]] <- c(active, t)
    if (direction != "forward") for (t in active) moves[[length(moves) + 1]] <- setdiff(active, t)
    if (length(moves) == 0) break
    cs <- sapply(moves, crit)
    o <- order(cs)
    gaps <- c(cur, cs[o[-1]]) - cs[o[1]]
    if (any(abs(gaps) < 1e-3)) stop("selection ", direction, " k=", k, ": a step is within 1e-3 of a tie")
    if (cs[o[1]] >= cur) break
    active <- moves[[o[1]]]
    cur <- cs[o[1]]
  }
  active
}
sc <- list(lower = ~1, upper = as.formula(paste("~", paste(preds, collapse = " + "))))
m0 <- polr(yf ~ 1, data = dsel, Hess = TRUE)
m1 <- polr(as.formula(paste("yf ~", paste(preds, collapse = " + "))), data = dsel, Hess = TRUE)
ord_selection_runs <- list()
for (crit_name in c("aic", "bic")) {
  k <- if (crit_name == "aic") 2 else log(nrow(dsel))
  for (direction in c("forward", "backward", "stepwise")) {
    st <- stepAIC(if (direction == "backward") m1 else m0, scope = sc, k = k, trace = 0,
                  direction = if (direction == "stepwise") "both" else direction)
    sel <- names(coef(st))
    mine <- ord_greedy(if (direction == "stepwise") "both" else direction, k)
    if (!setequal(sel, mine) || (direction == "forward" && !identical(sel, mine))) stop("stepAIC ", direction, " ", crit_name, " picks ", paste(sel, collapse = ","), ", the polished search ", paste(mine, collapse = ","))
    fit <- ord_fit_theta(dsel, sel, J)
    ord_selection_runs[[length(ord_selection_runs) + 1]] <- list(direction = direction, criterion = crit_name,
      selected = I(sel), steps = I(as.character(st$anova$Step)), coefficients = ord_g17(fit$theta),
      deviance = ord_num(fit$dev))
  }
}
ord_selection <- list(name = "selection_logit", predictors = preds, levels = as.character(seq_len(J)),
  n_obs = nrow(dsel), runs = ord_selection_runs, rows = ord_rows(dsel, preds))

write_golden("ordinal", list(
  generator = "scripts/reference/gen_ordinal.R",
  r_version = R.version.string,
  packages = as.list(sapply(names(ord_pins), function(p) as.character(packageVersion(p)))),
  note = paste(
    "coefficients = c(beta, zeta) of polr(y ~ x, method = logistic | probit | cloglog) polished to the MLE by Newton",
    "on the analytic gradient / Hessian (polr reltol 1e-14 agrees to 1e-6); vcov = the inverse analytic observed",
    "information (classical) or the hc0 / hc1 / cluster (CR1) sandwich on the per-row scores; deviance = -2 loglik,",
    "null_deviance = the thresholds-only fit's; fitted_probabilities = weighted mean P(Y = k | x); brant = brant::brant's",
    "statistic with the binary glms at their fixed point (omnibus, per-term); row_eta = x'beta and",
    "row_probabilities = P(Y = k | x) per row (predict(polr, type = 'probs') agrees to 1e-6). rows [x..., y, w, g];",
    "holdout = the weighted proper scores of the holdout_rows [x..., y, w, g] at the MLE: log_loss = mean -ln P(y),",
    "brier = mean sum_k (P_k - [y = k])^2, accuracy = mean [first argmax = y], rps = mean sum_{k<K} (F_k - [y <= k])^2 / (K - 1).",
    "weight '' (w = 1), frequency (= the rep() expansion) or probability (w* = w n_eff / sum w).",
    "exponentiated = the predictors' exp(beta), exp(beta) SE and exp(beta -/+ qnorm(0.975) SE) on the case's covariance.",
    "nominal_test = 2 (loglik partial - loglik po) of the partial proportional-odds fit (one predictor, then every",
    "predictor, non-parallel; ordinal::nominal_test / clm agree to 1e-6) polished to its MLE; null when the partial",
    "fit does not converge or crosses (nominal_test$failed)."),
  cases = ord_cases,
  modifiers = list(jackknife = ord_jackknife, selection = ord_selection)
))
