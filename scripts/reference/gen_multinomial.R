# gen_multinomial.R — R oracle for REG_MULTINOMIAL, the baseline-category
# (multinomial) logit (U65 E4-S2): nnet::multinom's fit pushed to the
# exact maximum-likelihood estimate.
#
# Writes <out_dir>/multinomial.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_multinomial.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. R 4.6.1 +
# jsonlite + MASS 7.3-66 (stepAIC, the modifiers block) + nnet 7.3-21
# (a recommended package this R build does not ship: install it into
# the user library from the Posit noble binary repo,
# docs/src/internals/regenerating-goldens.md, The R toolchain).
#
# The model, per row i with predictors x_i (intercept first) and L
# observed levels, the reference r:
#   eta_ik = x_i' beta_k (beta_r = 0),  pi_ik = exp(eta_ik) / sum_l exp(eta_il)
#   logLik = sum_i w_i log pi_{i, y_i}
# multinom(y ~ x1 + x2, Hess = TRUE, maxit = 1000, reltol = 1e-16) is the
# starting point; its BFGS stops a few 1e-8 off the MLE, so the script
# then takes Newton steps on the analytic gradient and Hessian
# (-d2 logLik = sum_i w_i (diag(pi_i) - pi_i pi_i') (x) x_i x_i') until
# the step is below 1e-15 relative — the MLE to double precision. The
# polished coefficients must agree with multinom's at 1e-6 and
# solve(Hessian) with vcov(multinom) at 1e-4 (multinom's Hessian is a
# finite difference of its gradient); both are asserted. Every figure is
# the polished fit's:
#   coefficients / std_errors / p_values / ci_*: category-major (each
#     non-reference level in level order, then (intercept), x1, x2) —
#     vcov(multinom)'s row order; SE = sqrt(diag(solve(H))), z = b / SE,
#     p = 2 pnorm(-|z|), ci = b -/+ qnorm(0.5 + conf/2) SE;
#   deviance = -2 logLik, null_deviance the intercept-only model's
#     (-2 sum_i w_i log(share of y_i)), two_log_lik = 2 logLik,
#     aic = deviance + 2 K (p + 1) (multinom's edf);
#   fitted_probabilities: every row's category probabilities at the
#     polished MLE (asserted equal to fitted(multinom) at 1e-6) — the
#     per-row vector an ATTR_REG_PREDICT score of a training row reads;
#   score_rows / score_probabilities / score_log_odds (U68 E1-S2): a
#     fixed grid of new [x1, x2] rows, their category probabilities
#     (asserted equal to predict(multinom, newdata, type = "probs") at
#     1e-6) and every category's log-odds against the reference
#     (x' beta_k; 0 for the reference) at the polished MLE;
#   holdout_rows / holdout (U68 E1-S3): the grid rows labelled [x1, x2,
#     y(, w)] and their weighted proper scores at the polished MLE
#     (mn_scores: log_loss, brier, accuracy); on empty_level one more row
#     at the dropped level, which no figure counts.
# Weights: "_freq" cases (integer f) are the rep()-expanded fit
# (asserted equal to multinom(weights = f)); "_prob" cases are
# multinom(weights = w*) with w* = w n_eff / sum(w), n_eff = (sum w)^2 /
# sum w^2 — REG_GLM's prior-weight rule — and carry no two_log_lik /
# aic (a pseudo-likelihood). A level with no row is dropped, as
# multinom drops it ("group is empty"); the reference defaults to the
# first observed level (multinom's) or is named (relevel()).
#
# Seeded (set.seed(20261102)). Rows are [x1, x2, y] or [x1, x2, y, w],
# y the 0-based index of the row's level in `levels` (the target's
# dictionary order).
#
# The `modifiers` block (U68 E2-S2) adds the resample / selection
# oracles — the manual-deletion jackknife and MASS::stepAIC (MASS
# 7.3-66) on multinom — drawn after every case under its own seed (see
# the block below).

suppressPackageStartupMessages({
  library(jsonlite)
  library(nnet)
  library(MASS)
})

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_multinomial.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("nnet")) != "7.3.21") stop("gen_multinomial.R pins nnet 7.3-21, got ", packageVersion("nnet"))
if (as.character(packageVersion("MASS")) != "7.3.66") stop("gen_multinomial.R pins MASS 7.3-66, got ", packageVersion("MASS"))

mn_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
mn_check <- function(what, got, want, tol) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  err <- max(abs(got - want) / pmax(1, abs(want)))
  if (err > tol) stop(what, ": ", err, " > ", tol)
  err
}

# mn_parts: per-row probabilities (n x L, reference column first in the
# fitted order), the weighted log-likelihood, the gradient and the
# negative Hessian of beta (K (p+1), category-major).
mn_parts <- function(beta, X, Yi, w) {
  k <- ncol(Yi) - 1; q <- ncol(X)
  B <- matrix(beta, nrow = q)               # q x K, column k = category k
  eta <- cbind(0, X %*% B)
  m <- apply(eta, 1, max)
  e <- exp(eta - m)
  P <- e / rowSums(e)
  ll <- sum(w * (rowSums(Yi * eta) - m - log(rowSums(e))))
  g <- as.vector(t(X) %*% (w * (Yi[, -1, drop = FALSE] - P[, -1, drop = FALSE])))
  H <- matrix(0, k * q, k * q)
  for (a in 1:k) for (b in 1:k) {
    s <- if (a == b) P[, a + 1] * (1 - P[, a + 1]) else -P[, a + 1] * P[, b + 1]
    H[(a - 1) * q + 1:q, (b - 1) * q + 1:q] <- t(X) %*% (X * (w * s))
  }
  list(P = P, ll = ll, g = g, H = H)
}

mn_cases <- list()

# The scored grid: new rows no fit saw, inside and outside the data's
# range (no RNG draw, so the fitted cases' data are unchanged).
mn_score_grid <- data.frame(x1 = c(-1, 0.5, 2.5, -0.25, 1.75, 0), x2 = c(-1.5, 1, 0.3, 2, -0.6, 0))

# The holdout rows (U68 E1-S3): the score grid again, each row labelled
# (cycling the fitted levels in dictionary order) and weighted on the
# weighted cases — a `holdout` predicate holds them out of the same fit.
# mn_scores folds their proper scores at the polished MLE: weighted
# means (w the raw slot weight) of -ln P(y) (natural log), sum_k (P_k -
# [y = k])^2 and [first argmax = y] in dictionary order (predict(multinom,
# type = "probs") agreeing to 1e-6). A held-out row labelled a level the
# fit dropped scores nothing (empty_level carries one).
mn_scores <- function(P, y, w) {
  n <- nrow(P)
  O <- outer(y, seq_len(ncol(P)), "==") * 1
  sw <- sum(w)
  list(n = n,
    log_loss = mn_g17(sum(w * -log(P[cbind(seq_len(n), y)])) / sw),
    brier = mn_g17(sum(w * rowSums((P - O)^2)) / sw),
    accuracy = mn_g17(sum(w * (max.col(P, ties.method = "first") == y)) / sw))
}

# mn_case fits y (a factor over `levels`, in that order) on x1 + x2.
mn_case <- function(name, d, levels, reference = NULL, weight = NULL, kind = NULL, conf = 0.95) {
  y <- factor(levels[d$y + 1], levels = levels)
  w <- rep(1, nrow(d))
  if (!is.null(weight)) {
    w <- weight
    if (kind == "probability") w <- w * (sum(w)^2 / sum(w^2)) / sum(w)
  }
  observed <- levels[levels %in% as.character(y)]
  ref <- if (is.null(reference)) observed[1] else reference
  fitted_levels <- c(ref, setdiff(observed, ref))
  yy <- factor(as.character(y), levels = fitted_levels)
  dd <- data.frame(x1 = d$x1, x2 = d$x2, yy = yy, w = w)
  m <- suppressWarnings(multinom(yy ~ x1 + x2, data = dd, weights = w, Hess = TRUE,
    maxit = 1000, reltol = 1e-16, trace = FALSE))
  X <- cbind(1, d$x1, d$x2)
  Yi <- sapply(fitted_levels, function(l) as.numeric(as.character(yy) == l))
  beta <- as.vector(t(coef(m)))
  for (it in 1:100) {
    pp <- mn_parts(beta, X, Yi, w)
    step <- solve(pp$H, pp$g)
    beta <- beta + step
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(beta)))) break
  }
  pp <- mn_parts(beta, X, Yi, w)
  if (max(abs(pp$g)) > 1e-9) stop(name, ": gradient not zero at the polished MLE")
  mn_check(paste(name, "multinom coefficients"), as.vector(t(coef(m))), beta, 1e-6)
  mn_check(paste(name, "fitted(multinom)"), as.vector(fitted(m)), as.vector(pp$P), 1e-6)
  Xs <- cbind(1, mn_score_grid$x1, mn_score_grid$x2)
  eta_s <- cbind(0, Xs %*% matrix(beta, nrow = ncol(X)))
  P_s <- exp(eta_s - apply(eta_s, 1, max))
  P_s <- P_s / rowSums(P_s)
  mn_check(paste(name, "predict(multinom, type = probs)"),
    as.vector(predict(m, newdata = mn_score_grid, type = "probs")), as.vector(P_s), 1e-6)
  V <- solve(pp$H)
  mn_check(paste(name, "multinom vcov"), as.vector(vcov(m)), as.vector(V), 1e-4)
  # Holdout: the grid rows labelled, P in dictionary order.
  nh <- nrow(mn_score_grid)
  observed_dict <- levels[levels %in% fitted_levels]
  yh_lab <- observed_dict[(seq_len(nh) - 1) %% length(observed_dict) + 1]
  wh <- if (is.null(kind)) rep(1, nh) else if (kind == "frequency") (seq_len(nh) - 1) %% 3 + 1 else 0.25 * (seq_len(nh) %% 5 + 2)
  Pd <- P_s[, match(observed_dict, fitted_levels), drop = FALSE]
  holdout <- mn_scores(Pd, match(yh_lab, observed_dict), wh)
  hrows <- lapply(seq_len(nh), function(i) {
    r <- c(mn_score_grid$x1[i], mn_score_grid$x2[i], match(yh_lab[i], levels) - 1)
    if (!is.null(weight)) r <- c(r, wh[i])
    mn_g17(r)
  })
  dropped <- setdiff(levels, observed)
  if (length(dropped) > 0) {
    # A held-out row at a level the fit dropped: not scored.
    r <- c(0.25, -0.5, match(dropped[1], levels) - 1)
    if (!is.null(weight)) r <- c(r, 1)
    hrows[[nh + 1]] <- mn_g17(r)
  }
  if (!is.null(weight) && kind == "frequency") {
    ex <- dd[rep(seq_len(nrow(dd)), weight), ]; ex$w <- 1
    me <- suppressWarnings(multinom(yy ~ x1 + x2, data = ex, Hess = TRUE, maxit = 1000,
      reltol = 1e-16, trace = FALSE))
    mn_check(paste(name, "weighted vs expanded coefficients"), as.vector(t(coef(me))), beta, 1e-6)
    mn_check(paste(name, "weighted vs expanded deviance"), deviance(me), -2 * pp$ll, 1e-9)
  }
  se <- sqrt(diag(V))
  z <- beta / se
  q <- qnorm(0.5 + conf / 2)
  shares <- colSums(Yi * w) / sum(w)
  null_dev <- -2 * sum(w * (Yi %*% log(shares)))
  k <- length(fitted_levels) - 1
  keys <- as.vector(t(outer(fitted_levels[-1], c("(intercept)", "x1", "x2"), paste, sep = ":")))
  rows <- lapply(seq_len(nrow(d)), function(i) {
    r <- c(d$x1[i], d$x2[i], d$y[i])
    if (!is.null(weight)) r <- c(r, weight[i])
    mn_g17(r)
  })
  doc <- list(
    name = name, levels = levels, categories = fitted_levels, reference = ref,
    reference_requested = !is.null(reference),
    weight_kind = if (is.null(kind)) "" else kind, conf_level = conf, rows = rows,
    keys = keys,
    coefficients = mn_g17(beta), std_errors = mn_g17(se), p_values = mn_g17(2 * pnorm(-abs(z))),
    ci_lower = mn_g17(beta - q * se), ci_upper = mn_g17(beta + q * se),
    vcov = mn_g17(as.vector(V)),
    deviance = mn_g17(-2 * pp$ll), null_deviance = mn_g17(null_dev),
    n_obs = sum(w > 0), fitted_probabilities = mn_g17(as.vector(t(pp$P))),
    score_rows = lapply(seq_len(nrow(mn_score_grid)), function(i) mn_g17(c(mn_score_grid$x1[i], mn_score_grid$x2[i]))),
    score_probabilities = mn_g17(as.vector(t(P_s))), score_log_odds = mn_g17(as.vector(t(eta_s))),
    holdout_rows = hrows, holdout = holdout)
  if (is.null(kind) || kind == "frequency") {
    doc$two_log_lik <- mn_g17(2 * pp$ll)
    doc$aic <- mn_g17(-2 * pp$ll + 2 * k * 3)
    mn_check(paste(name, "multinom AIC"), m$AIC, -2 * pp$ll + 2 * k * 3, 1e-8)
  }
  mn_cases[[length(mn_cases) + 1]] <<- doc
}

set.seed(20261102)
mn_draw <- function(n, B) {
  d <- data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(rnorm(n), 3))
  eta <- cbind(0, cbind(1, d$x1, d$x2) %*% B)
  P <- exp(eta) / rowSums(exp(eta))
  d$y <- apply(P, 1, function(p) sample.int(length(p), 1, prob = p)) - 1
  d
}

d <- mn_draw(150, cbind(c(0.3, 0.8, -0.5), c(-0.4, 1.1, 0.6)))
mn_case("three_levels", d, c("a", "b", "c"))
mn_case("three_levels_conf90", d, c("a", "b", "c"), conf = 0.90)

# Dictionary order is not alphabetical; the reference is named.
d <- mn_draw(200, cbind(c(0.2, -0.6, 0.4), c(0.5, 0.3, -0.8), c(-0.3, 0.9, 0.2)))
mn_case("four_levels_reference", d, c("low", "mid", "high", "top"), reference = "high")

d <- mn_draw(60, cbind(c(0.1, 0.7, -0.4), c(-0.2, -0.5, 0.9)))
mn_case("three_levels_freq", d, c("a", "b", "c"), weight = sample(1:4, 60, replace = TRUE), kind = "frequency")
mn_case("three_levels_prob", d, c("a", "b", "c"), weight = round(runif(60, 0.2, 3), 3), kind = "probability")

# A dictionary level no row carries is dropped (multinom's "group is empty").
d <- mn_draw(90, cbind(c(0.2, 0.6, 0.3), c(-0.1, -0.7, 0.5)))
d$y <- ifelse(d$y == 0, 0, d$y + 1)
mn_case("empty_level", d, c("a", "unused", "b", "c"))

# Resample and selection (U68 E2-S2, #658): REG_MULTINOMIAL refit by the
# engine's modifier drivers, every refit at the full fit's categories and
# reference. Drawn AFTER every case above under its own seed, so no
# case's rows move. Rows are [x..., y, g] (y the 0-based level index, g
# a cluster id).
#   * jackknife: the manual-deletion jackknife on multinom — n
#     leave-one-out refits (each Newton-polished to its MLE from the full
#     fit and checked against multinom on the same deletion to 1e-6), SE
#     = sqrt((n - 1)/n sum_i (beta_(i) - mean)^2) over every coefficient;
#     jackknife_by g: one refit per deleted cluster (sorted values),
#     (G - 1)/G. Every deletion keeps every category (asserted).
#   * selection: MASS::stepAIC on multinom — forward and both from y ~ 1,
#     backward from the full model, scope the five predictors, k = 2 (AIC)
#     and log(n) (BIC); a term enters or leaves across every non-reference
#     category at once, and the criterion is extractAIC's deviance + k *
#     edf, edf = (K - 1) (terms + 1). A greedy search over the POLISHED
#     deviances re-runs each path and must pick the same terms, every
#     step's winner ahead of the runner-up by more than 1e-3 (so
#     multinom's ~1e-8 optimum cannot flip a step); selected lists the
#     terms in stepAIC's order, coefficients the final model's polished
#     MLE (category-major over (intercept) then the selected terms).
set.seed(20261129)
mn_mdraw <- function(n, B, p) {
  d <- as.data.frame(matrix(round(rnorm(n * p), 3), n, p))
  names(d) <- paste0("x", seq_len(p))
  X <- cbind(1, as.matrix(d))
  eta <- cbind(0, X %*% B)
  P <- exp(eta) / rowSums(exp(eta))
  d$y <- apply(P, 1, function(pp) sample.int(length(pp), 1, prob = pp)) - 1
  d$g <- sample(1:8, n, replace = TRUE)
  d
}
mn_mlevels <- c("a", "b", "c")
# mn_mfit: multinom of y on preds over d (reference "a"), Newton-polished
# from start (default: multinom's own optimum); returns beta and the
# deviance.
mn_mfit <- function(d, preds, start = NULL) {
  yy <- factor(mn_mlevels[d$y + 1], levels = mn_mlevels)
  if (any(table(yy) == 0)) stop("modifier refit lost a category")
  dd <- data.frame(d[, preds, drop = FALSE], yy = yy)
  f <- if (length(preds) == 0) yy ~ 1 else as.formula(paste("yy ~", paste(preds, collapse = " + ")))
  m <- multinom(f, data = dd, maxit = 1000, reltol = 1e-16, trace = FALSE)
  X <- cbind(1, as.matrix(d[, preds, drop = FALSE]))
  Yi <- sapply(mn_mlevels, function(l) as.numeric(as.character(yy) == l))
  w <- rep(1, nrow(d))
  beta <- if (is.null(start)) as.vector(t(coef(m))) else start
  for (it in 1:100) {
    pp <- mn_parts(beta, X, Yi, w)
    step <- solve(pp$H, pp$g)
    beta <- beta + step
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(beta)))) break
  }
  pp <- mn_parts(beta, X, Yi, w)
  if (max(abs(pp$g)) > 1e-9) stop("modifier refit: gradient not zero at the polished MLE")
  mn_check("modifier refit multinom vs polished", as.vector(t(coef(m))), beta, 1e-6)
  list(beta = beta, dev = -2 * pp$ll)
}
mn_jk_se <- function(reps, scale) {
  mu <- colMeans(reps)
  sqrt(scale * colSums(sweep(reps, 2, mu)^2))
}
mn_mrows <- function(d, preds) lapply(seq_len(nrow(d)), function(i) mn_g17(c(unlist(d[i, preds]), d$y[i], d$g[i])))
mn_mkeys <- function(preds) as.vector(t(outer(mn_mlevels[-1], c("(intercept)", preds), paste, sep = ":")))

d <- mn_mdraw(80, cbind(c(0.2, 0.9, -0.5), c(-0.3, -0.6, 0.8)), 2)
preds <- c("x1", "x2")
full <- mn_mfit(d, preds)
loo <- t(sapply(seq_len(nrow(d)), function(i) mn_mfit(d[-i, ], preds, full$beta)$beta))
gs <- sort(unique(d$g))
grp <- t(sapply(gs, function(g) mn_mfit(d[d$g != g, ], preds, full$beta)$beta))
mn_jackknife <- list(name = "jackknife", levels = mn_mlevels, predictors = preds, keys = mn_mkeys(preds),
  coefficients = mn_g17(full$beta),
  std_errors = mn_g17(mn_jk_se(loo, (nrow(d) - 1) / nrow(d))),
  by_groups = length(gs),
  by_std_errors = mn_g17(mn_jk_se(grp, (length(gs) - 1) / length(gs))),
  rows = mn_mrows(d, preds))

d <- mn_mdraw(220, cbind(c(0.3, 1.0, -0.8, 0.25, 0, 0), c(-0.2, -0.7, 0.9, 0, 0, 0)), 5)
preds <- paste0("x", 1:5)
dsel <- d
dsel$yy <- factor(mn_mlevels[dsel$y + 1], levels = mn_mlevels)
K1 <- length(mn_mlevels) - 1
# mn_greedy re-runs one stepAIC path over polished deviances; it returns
# the terms in entry order, stopping when no move lowers the criterion
# and refusing a step whose winner leads the runner-up (or the stay) by
# less than 1e-3.
mn_greedy <- function(direction, k) {
  crit <- function(active) mn_mfit(dsel, active)$dev + k * K1 * (length(active) + 1)
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
m0 <- multinom(yy ~ 1, data = dsel, maxit = 1000, reltol = 1e-16, trace = FALSE)
m1 <- multinom(as.formula(paste("yy ~", paste(preds, collapse = " + "))), data = dsel, maxit = 1000, reltol = 1e-16, trace = FALSE)
mn_selection_runs <- list()
for (crit_name in c("aic", "bic")) {
  k <- if (crit_name == "aic") 2 else log(nrow(dsel))
  for (direction in c("forward", "backward", "stepwise")) {
    st <- stepAIC(if (direction == "backward") m1 else m0, scope = sc, k = k, trace = 0,
                  direction = if (direction == "stepwise") "both" else direction)
    sel <- setdiff(colnames(coef(st)), "(Intercept)")
    mine <- mn_greedy(if (direction == "stepwise") "both" else direction, k)
    if (!setequal(sel, mine) || (direction == "forward" && !identical(sel, mine))) stop("stepAIC ", direction, " ", crit_name, " picks ", paste(sel, collapse = ","), ", the polished search ", paste(mine, collapse = ","))
    fit <- mn_mfit(dsel, sel)
    mn_check(paste("stepAIC", direction, crit_name, "extractAIC"), extractAIC(st, k = k)[2], fit$dev + k * K1 * (length(sel) + 1), 1e-6)
    mn_selection_runs[[length(mn_selection_runs) + 1]] <- list(direction = direction, criterion = crit_name,
      selected = I(sel), steps = I(as.character(st$anova$Step)), keys = I(mn_mkeys(sel)),
      coefficients = mn_g17(fit$beta), deviance = mn_g17(fit$dev))
  }
}
mn_selection <- list(name = "selection", levels = mn_mlevels, predictors = preds,
  n_obs = nrow(dsel), runs = mn_selection_runs, rows = mn_mrows(dsel, preds))

write_golden("multinomial", list(
  generator = "scripts/reference/gen_multinomial.R",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")), jsonlite = as.character(packageVersion("jsonlite")),
    nnet = as.character(packageVersion("nnet")), MASS = as.character(packageVersion("MASS"))),
  note = paste(
    "multinom(y ~ x1 + x2) Newton-polished to the MLE. keys category-major: <level>:(intercept), <level>:x1, <level>:x2 per",
    "non-reference level in fitted order (categories: reference first, then the observed levels in dictionary order);",
    "vcov row-major over keys; fitted_probabilities row-major n x L over categories;",
    "score_probabilities / score_log_odds row-major (score rows) x L over categories, log-odds vs the reference;",
    "holdout = the weighted proper scores of holdout_rows [x1, x2, y(, w)] (a row at a dropped level scores nothing):",
    "log_loss = mean -ln P(y), brier = mean sum_k (P_k - [y = k])^2, accuracy = mean [first argmax = y] in dictionary order."),
  cases = mn_cases,
  modifiers = list(jackknife = mn_jackknife, selection = mn_selection)
))
