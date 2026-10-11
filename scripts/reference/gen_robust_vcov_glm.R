# gen_robust_vcov_glm.R — R oracle for REG_GLM and REG_BAYES_LINEAR
# robust coefficient covariance (RegressionSpec.Covariance: hc0-hc3,
# cluster CR1, hac Newey-West; U25 E5-S2).
#
# Sourced by gen_multivariate.R (`make reference`) after
# gen_robust_vcov.R: it reuses out_dir, num(), numv(), numm(), strs(),
# near(), expand(), n_star(), write_golden(), rv_meta() and auto_lags().
# Writes <out_dir>/mv_robust_vcov_glm.json. CI never runs R: everything
# here is committed. A rerun on the pinned toolchain is byte-identical;
# it draws its own seeded rows AFTER every other golden, so no earlier
# golden moves.
#
# Rows: n = 60 in SHUFFLED row order (week orders the series), six
# regions of ten rows (the clusters), a region effect and an AR(1)
# latent term shared by three responses -- y_bin (logit), y_count
# (log, overdispersed) and y (linear, heteroskedastic) -- plus two
# weight columns. Predictors x1, x2 (k = 3 with the intercept).
#
# REG_GLM (binomial / logit, poisson / log). b is the MLE on prior
# weights w* = w * N*/sum(w) (N* = sum(w) frequency, Kish n_eff
# probability), Newton-polished from glm.fit to a fixed point (R's
# glm keeps the working weights one step behind its coefficients).
# At b: mu, me = dmu/deta, V(mu); bread B = (X' diag(w* me^2/V) X)^-1;
# unit-weight score g_i = me_i (y_i - mu_i)/V_i x_i; unit leverage
# h_i = me_i^2/V_i x_i' B x_i. The meat is gen_robust_vcov.R's with
# e_i x_i replaced by g_i (hc: sum omega w* g g'; cluster: s_g = sum
# w* g; hac: u_t = w*_t g_t), the same N* adjustments. Inference is
# Wald z (lmtest::coeftest's default on a glm): p = 2 pnorm(-|b/se|),
# ci at qnorm(0.975). Asserted: unweighted == sandwich::vcovHC /
# vcovCL(type = "HC1") / NeweyWest(prewhite = FALSE, adjust = TRUE) on
# glm refit from b (start = b) and coeftest's p-values; frequency hc /
# cluster == the same calls on the rep()-expanded glm; weighted hac ==
# NeweyWest(glm(weights = w*), adjust = FALSE) * N*/(N* - k).
#
# REG_BAYES_LINEAR (Muller 2013 sandwich posterior; frequency weights
# only). The conjugate NIG posterior under Pulse's default prior
# (mu0 = 0, precision eps = 1e-3, a0 = b0 = 1e-3): Lambda_n = eps I +
# X'WX, mu_n = Lambda_n^-1 X'Wy. The coefficients stay mu_n; the
# covariance is the sandwich with bread Lambda_n^-1 (the posterior
# precision, sigma^2-free), score g_i = (y_i - x_i' mu_n) x_i and
# leverage h_i = x_i' Lambda_n^-1 x_i, the same meats and adjustments.
# Muller's sandwich posterior is Gaussian: ci = mu_n -/+ qnorm(0.975)
# se, no p-values. Asserted: at eps = 0 the formula is
# sandwich::vcovHC / vcovCL / NeweyWest on lm (the hand computation is
# the sandwich); frequency == the same posterior on the rep()-expanded
# rows (hc / cluster).

set.seed(906)
n_gb <- 60
gb_regions <- paste0("zone_", LETTERS[1:6])
gb_rid <- sample(rep(1:6, each = 10))
gx1 <- round(rnorm(n_gb), 3)
gx2 <- round(runif(n_gb, 0, 2), 3)
g_eff <- rnorm(6, 0, 0.5)[gb_rid]
g_ar <- numeric(n_gb)
g_ar[1] <- rnorm(1, 0, 0.5)
for (t in 2:n_gb) g_ar[t] <- 0.5 * g_ar[t - 1] + rnorm(1, 0, 0.5)
g_bin <- rbinom(n_gb, 1, plogis(-0.2 + 0.9 * gx1 - 0.6 * gx2 + g_eff + g_ar))
g_count <- rpois(n_gb, exp(0.3 + 0.4 * gx1 + 0.3 * gx2 + g_eff + g_ar))
g_y <- round(1 + 1.5 * gx1 - 0.8 * gx2 + g_eff + g_ar + rnorm(n_gb, 0, 0.3 + 0.4 * gx2), 3)
g_wf <- sample(1:4, n_gb, replace = TRUE)
g_wp <- round(runif(n_gb, 0.2, 3), 3)
gb <- data.frame(week = 1:n_gb, region = gb_regions[gb_rid], x1 = gx1, x2 = gx2,
                 y_bin = g_bin, y_count = g_count, y = g_y, w_freq = g_wf, w_prob = g_wp,
                 stringsAsFactors = FALSE)
gb <- gb[sample(n_gb), ]
rownames(gb) <- NULL
fml_gb <- ~ x1 + x2

# robust_meat is the sandwich meat and adjustment over unit scores G
# (rows), prior weights ws, unit leverage h; d carries region / week.
robust_meat <- function(type, G, ws, h, d, ns, k, lags) {
  G_ <- NA; L <- NA
  if (type %in% c("hc0", "hc1", "hc2", "hc3")) {
    om <- switch(type, hc0 = 1, hc1 = 1, hc2 = 1 / (1 - h), hc3 = 1 / (1 - h)^2)
    M <- crossprod(G * (om * ws), G)
    adj <- if (type == "hc1") ns / (ns - k) else 1
  } else if (type == "cluster") {
    S <- rowsum(G * ws, d$region)
    G_ <- nrow(S)
    M <- crossprod(S)
    adj <- G_ / (G_ - 1) * (ns - 1) / (ns - k)
  } else {
    o <- order(d$week)
    U <- (G * ws)[o, , drop = FALSE]
    L <- if (is.null(lags)) auto_lags(nrow(d)) else lags
    M <- crossprod(U)
    for (l in seq_len(L)) {
      if (l >= nrow(U)) break
      bw <- 1 - l / (L + 1)
      Gl <- crossprod(U[(l + 1):nrow(U), , drop = FALSE], U[1:(nrow(U) - l), , drop = FALSE])
      M <- M + bw * (Gl + t(Gl))
    }
    adj <- ns / (ns - k)
  }
  list(M = M, adj = adj, G = G_, L = L)
}

# glm_mle is the prior-weighted MLE polished to a fixed point (see
# gen_multivariate.R's glm_fixed_point) and its sandwich ingredients.
glm_mle <- function(X, y, ws, fam) {
  ctl <- glm.control(epsilon = 1e-14, maxit = 200)
  b <- suppressWarnings(glm.fit(X, y, weights = ws, family = fam, control = ctl))$coefficients
  last <- Inf
  for (it in 1:100) {
    eta <- drop(X %*% b); mu <- fam$linkinv(eta); me <- fam$mu.eta(eta)
    w <- ws * me^2 / fam$variance(mu)
    step <- drop(solve(crossprod(X * w, X), crossprod(X * w, (y - mu) / me)))
    rel <- max(abs(step) / pmax(abs(b), 1))
    b <- b + step
    if (rel == 0 || (rel <= 1e-14 && rel >= last)) break
    last <- rel
  }
  if (rel > 1e-14) stop("glm fixed point not reached: relative step ", rel)
  eta <- drop(X %*% b); mu <- fam$linkinv(eta); me <- fam$mu.eta(eta); va <- fam$variance(mu)
  B <- solve(crossprod(X * (ws * me^2 / va), X))
  list(b = b, B = B, G = X * (me * (y - mu) / va), h = (me^2 / va) * rowSums((X %*% B) * X))
}

# R's sandwich estimators on a fitted model, for the self-checks.
r_sandwich <- function(fit, type, d, L, adjust = TRUE) {
  switch(type,
    hc0 = vcovHC(fit, type = "HC0"), hc1 = vcovHC(fit, type = "HC1"),
    hc2 = vcovHC(fit, type = "HC2"), hc3 = vcovHC(fit, type = "HC3"),
    cluster = vcovCL(fit, cluster = d$region, type = "HC1"),
    hac = NeweyWest(fit, lag = L, order.by = d$week, prewhite = FALSE, adjust = adjust))
}

# bayes_post is the NIG posterior mean and the sandwich ingredients at
# prior precision eps.
bayes_post <- function(X, y, ws, eps) {
  Linv <- solve(crossprod(X * ws, X) + diag(eps, ncol(X)))
  mun <- drop(Linv %*% crossprod(X * ws, y))
  list(b = mun, B = Linv, G = X * drop(y - X %*% mun), h = rowSums((X %*% Linv) * X))
}

gb_cases <- list()
gb_case <- function(family, kind, type, lags = NULL) {
  d <- gb
  target <- switch(family, binomial = "y_bin", poisson = "y_count", bayes = "y")
  w <- switch(kind, none = rep(1, nrow(d)), frequency = d$w_freq, probability = d$w_prob)
  ns <- n_star(w, if (kind == "none") "frequency" else kind)
  ws <- w * ns / sum(w)
  X <- model.matrix(fml_gb, d); yv <- d[[target]]; k <- ncol(X)
  fam <- switch(family, binomial = binomial(), poisson = poisson(), bayes = NULL)
  f <- if (is.null(fam)) bayes_post(X, yv, ws, 1e-3) else glm_mle(X, yv, ws, fam)
  rm_ <- robust_meat(type, f$G, ws, f$h, d, ns, k, lags)
  V <- f$B %*% rm_$M %*% f$B * rm_$adj
  b <- f$b
  dimnames(V) <- list(colnames(X), colnames(X)); names(b) <- colnames(X)
  se <- sqrt(diag(V))
  q <- qnorm(0.975)
  pv <- 2 * pnorm(-abs(b / se))
  L <- rm_$L

  # --- self-checks against sandwich / lmtest
  if (!is.null(fam)) {
    fml_t <- reformulate(c("x1", "x2"), target)
    ctl <- glm.control(epsilon = 1e-14, maxit = 200)
    if (kind == "none") {
      fit <- glm(fml_t, family = fam, data = d, start = b, control = ctl)
      near(r_sandwich(fit, type, d, L), V, 1e-8, paste("glm sandwich", family, type))
      near(coeftest(fit, vcov. = V)[, 4], pv, 1e-10, paste("coeftest p", family, type))
    }
    if (kind == "frequency" && type != "hac") {
      ed <- expand(d, w)
      fe <- glm(fml_t, family = fam, data = ed, start = b, control = ctl)
      near(r_sandwich(fe, type, ed, L), V, 1e-8, paste("expanded glm sandwich", family, type))
    }
    if (kind != "none" && type == "hac") {
      d$.ws <- ws
      fw <- suppressWarnings(glm(fml_t, family = fam, data = d, weights = .ws, start = b, control = ctl))
      near(r_sandwich(fw, type, d, L, adjust = FALSE) * ns / (ns - k), V, 1e-8, paste("weighted glm NeweyWest", family, kind))
    }
  } else {
    if (kind == "none") {
      f0 <- bayes_post(X, yv, ws, 0)
      r0 <- robust_meat(type, f0$G, ws, f0$h, d, ns, k, lags)
      fit <- lm(reformulate(c("x1", "x2"), target), data = d)
      near(r_sandwich(fit, type, d, L), f0$B %*% r0$M %*% f0$B * r0$adj, 1e-10, paste("bayes eps=0 vs sandwich lm", type))
    }
    if (kind == "frequency" && type != "hac") {
      ed <- expand(d, w)
      Xe <- model.matrix(fml_gb, ed)
      fe <- bayes_post(Xe, ed[[target]], rep(1, nrow(ed)), 1e-3)
      re <- robust_meat(type, fe$G, rep(1, nrow(ed)), fe$h, ed, nrow(ed), k, lags)
      near(fe$B %*% re$M %*% fe$B * re$adj, V, 1e-9, paste("expanded bayes sandwich", type))
    }
  }

  gb_cases[[length(gb_cases) + 1]] <<- Filter(Negate(is.null), list(
    family = family, target = target, weight = kind, covariance = type,
    lags = if (is.null(lags)) (if (type == "hac") "auto" else NULL) else num(lags),
    lags_used = if (is.na(L)) NULL else num(L),
    clusters = if (is.na(rm_$G)) NULL else num(rm_$G),
    n_rows = num(nrow(d)), n_star = num(ns),
    terms = strs(colnames(X)), coefficients = numv(b), std_errors = numv(se),
    p_values = if (is.null(fam)) NULL else numv(pv),
    ci_lower = numv(b - q * se), ci_upper = numv(b + q * se),
    vcov = numm(V)
  ))
}
for (family in c("binomial", "poisson", "bayes")) {
  kinds <- if (family == "bayes") c("none", "frequency") else c("none", "frequency", "probability")
  for (kind in kinds) {
    for (type in c("hc0", "hc1", "hc2", "hc3", "cluster")) gb_case(family, kind, type)
    gb_case(family, kind, "hac", 2)
    gb_case(family, kind, "hac")
  }
}

gb_rows <- lapply(seq_len(nrow(gb)), function(i) list(
  week = num(gb$week[i]), region = gb$region[i],
  x1 = num(gb$x1[i]), x2 = num(gb$x2[i]),
  y_bin = num(gb$y_bin[i]), y_count = num(gb$y_count[i]), y = num(gb$y[i]),
  w_freq = num(gb$w_freq[i]), w_prob = num(gb$w_prob[i])))

gb_doc <- c(rv_meta("robust GLM and Bayes linear coefficient covariance",
  "sandwich::vcovHC(glm, type = HC0..HC3), vcovCL(glm, cluster, type = 'HC1'), NeweyWest(glm, lag, order.by, prewhite = FALSE, adjust = TRUE); lmtest::coeftest(glm, vcov); Bayes: the Muller (2013) sandwich posterior in closed form",
  "family binomial (y_bin ~ x1 + x2, logit), poisson (y_count, log) or bayes (y, REG_BAYES_LINEAR under the default prior: mu0 = 0, precision 1e-3). Rows shuffled; week orders the series, region the six clusters. Weights follow the one rule (w* = w * N*/sum(w)); GLM w* are prior weights; Bayes is frequency-only. GLM: the sandwich over IRLS scores at the converged weights, Wald z (p_values = 2 pnorm(-|b/se|)). Bayes: coefficients the posterior mean, vcov the sandwich with bread Lambda_n^-1, no p-values. ci at 0.95 on qnorm(0.975). lags: the request, lags_used the resolved floor(4 (n/100)^(2/9))."),
  list(rows = gb_rows, cases = gb_cases))
write_golden("mv_robust_vcov_glm", gb_doc)
