# gen_mmm_hill.R — R oracle for REG_MMM_HILL (U64 E4-S1, #619): the
# Hill media-mix least-squares fit pinned to minpack.lm on the same
# objective, the same bounds and the same start.
#
# Writes <out_dir>/mmm_hill.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir, write_golden() and num();
# runnable on its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_mmm_hill.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Pins R 4.6.1,
# minpack.lm 1.2-4 (the version optimize/testdata/gen_optimize.R pins)
# and jsonlite 2.0.0. Seeded RNG; every input is rounded to 4 decimals
# and stored, so the Go test fits the very rows R fitted.
#
# The model (REG_MMM_HILL's, internal/regmmm/hill.go):
#
#   y_i = b0 + sum_c beta_c * hill(a_ci, k_c, s_c) + sum_j gamma_j * z_ji
#   hill(x, k, s) = 1 / (1 + (k / x)^s)      (0 at x = 0)
#
# minimising 1/2 * sum r_i^2 (+ 1/2 * precision * (beta_p - mean)^2 under
# a prior, an extra pseudo-residual row), with beta_c >= 0 (nonnegative,
# the default), k_c in [0.02, 20] * mean(a_c) and s_c in [0.5, 5]. Pulse
# searches k and s on the log scale (k = mean(a_c) * exp(theta_k), s =
# exp(theta_s)); R searches them directly. The objective, the bounds and
# so the optimum are the same; the start is the same point: k_c =
# mean(a_c), s_c = 1, and [b0, beta, gamma] by least squares on the Hill
# columns there (a beta <= 0 starts at 1e-3, a prior channel at its
# prior mean).
#
# Self-checks before anything is written (the oracle must be the
# optimum, not merely R's stopping point; the pattern of gen_optimize.R):
#   * nlsLM (formula interface, finite-difference Jacobian, bounds) and
#     nls.lm (the same MINPACK solver on the analytic Jacobian) agree to
#     1e-7 relative;
#   * Gauss-Newton polishing on the analytic Jacobian moves the nls.lm x
#     by under 1e-7 relative; the POLISHED point is the stored x, its
#     gradient J'r is 0 to 1e-9 of |J_j| |r| on every free parameter, and
#     no free parameter sits on a bound;
#   * a held case (a channel whose unconstrained beta is negative):
#     minpack.lm holds a bound by clamping the step, so the oracle is the
#     REDUCED problem (the channel's beta fixed at 0, which drops its k
#     and s from the objective). The KKT sign is checked on a grid over
#     the channel's whole (k, s) box: the cost gradient in beta is > 0
#     everywhere, so beta = 0 is the constrained optimum whatever k and s
#     the solver leaves the channel at (they are not identified; Pulse
#     reports them with null standard errors and the test does not pin
#     them).
#
# Figures, as REG_MMM_HILL defines them (internal/regmmm/hill.go):
#   coefficients     (intercept), betas, controls, k:<c>, s:<c>
#   std_errors       sqrt(diag(sigma^2 (J'J)^-1)) at the polished x in
#                    (k, s) units, sigma^2 = SSR / (n - p) with SSR over
#                    the data rows (no prior pseudo-residual) and p EVERY
#                    parameter (a held channel's three included); J'J
#                    includes the prior row. Equal to summary(nlsLM)'s
#                    at 1e-6 (its finite-difference J) on the unheld,
#                    prior-free cases. Free parameters only.
#   deviance         SSR;  r2 = 1 - SSR / TSS;  residual_std_err =
#                    sqrt(SSR / (n - p));  df = n - p.

suppressPackageStartupMessages({
  library(jsonlite)
  library(minpack.lm)
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
if (!exists("num")) {
  num <- function(x) {
    if (length(x) != 1) stop("num() takes a scalar")
    if (!is.finite(x)) stop("non-finite reference value: ", x)
    structure(sprintf("%.17g", x), class = "json")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_mmm_hill.R pins R 4.6.1, got ", R.version.string)
mh_pins <- c(minpack.lm = "1.2.4", jsonlite = "2.0.0")
for (mh_p in names(mh_pins)) {
  if (as.character(packageVersion(mh_p)) != mh_pins[[mh_p]]) stop("gen_mmm_hill.R pins ", mh_p, " ", mh_pins[[mh_p]], ", found ", packageVersion(mh_p))
}

mh_vec <- function(v) lapply(unname(as.numeric(v)), num)
mh_near <- function(a, b, tol, what, floor = 0) {
  a <- as.numeric(a); b <- as.numeric(b)
  scale <- pmax(abs(a), abs(b), floor)
  ok <- abs(a - b) <= tol * scale | scale == 0
  if (!all(ok)) stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / scale))
}
mh_hill <- function(x, k, s) ifelse(x > 0, 1 / (1 + (k / x)^s), 0)
mh_tight <- nls.lm.control(ftol = 1e-15, ptol = 1e-15, gtol = 0, maxiter = 1000, maxfev = 100000)

mh_cases <- list()

# mh_case fits one problem. a: list of channel columns (named); z: list
# of control columns (named); prior: NULL or list(channel, mean,
# precision); held: channel names whose beta is expected at 0.
mh_case <- function(name, y, a, z, prior = NULL, held = character(0)) {
  n <- length(y)
  ch <- names(a); ct <- names(z)
  nc <- length(ch); nz <- length(ct)
  pn <- c("(intercept)", ch, ct, paste0("k:", ch), paste0("s:", ch))
  kref <- vapply(a, mean, 0)
  lower <- c(-Inf, rep(0, nc), rep(-Inf, nz), 0.02 * kref, rep(0.5, nc))
  upper <- c(Inf, rep(Inf, nc), rep(Inf, nz), 20 * kref, rep(5, nc))
  names(lower) <- names(upper) <- pn
  pidx <- if (is.null(prior)) 0L else match(prior$channel, ch)

  # The model on the full parameter vector p (pn order).
  pieces <- function(p) {
    b0 <- p[[1]]; b <- p[1 + seq_len(nc)]; g <- p[1 + nc + seq_len(nz)]
    k <- p[1 + nc + nz + seq_len(nc)]; s <- p[1 + 2 * nc + nz + seq_len(nc)]
    list(b0 = b0, b = b, g = g, k = k, s = s)
  }
  pred <- function(p) {
    q <- pieces(p)
    out <- rep(q$b0, n)
    for (c in seq_len(nc)) out <- out + q$b[c] * mh_hill(a[[c]], q$k[c], q$s[c])
    for (j in seq_len(nz)) out <- out + q$g[j] * z[[j]]
    out
  }
  fn <- function(p) {
    r <- pred(p) - y
    if (pidx > 0) r <- c(r, sqrt(prior$precision) * (p[[1 + pidx]] - prior$mean))
    r
  }
  jac <- function(p) {
    q <- pieces(p)
    J <- matrix(0, n, length(pn), dimnames = list(NULL, pn))
    J[, 1] <- 1
    for (c in seq_len(nc)) {
      h <- mh_hill(a[[c]], q$k[c], q$s[c])
      hh <- h * (1 - h)
      lx <- ifelse(a[[c]] > 0, log(a[[c]] / q$k[c]), 0)
      J[, 1 + c] <- h
      J[, 1 + nc + nz + c] <- -q$b[c] * q$s[c] / q$k[c] * hh
      J[, 1 + 2 * nc + nz + c] <- q$b[c] * lx * hh
    }
    for (j in seq_len(nz)) J[, 1 + nc + j] <- z[[j]]
    if (pidx > 0) {
      pr <- numeric(length(pn)); pr[1 + pidx] <- sqrt(prior$precision)
      J <- rbind(J, pr)
    }
    J
  }

  # Pulse's start: k at the channel mean, s = 1, the linear part by
  # least squares on the Hill columns there.
  X0 <- cbind(1, sapply(seq_len(nc), function(c) mh_hill(a[[c]], kref[c], 1)), if (nz > 0) do.call(cbind, z))
  lin0 <- solve(crossprod(X0), crossprod(X0, y))[, 1]
  start <- c(lin0, kref, rep(1, nc))
  names(start) <- pn
  for (c in seq_len(nc)) {
    if (c == pidx && prior$precision > 0) start[[1 + c]] <- prior$mean
    if (!(start[[1 + c]] > 0)) start[[1 + c]] <- 1e-3
  }

  # The reduced problem: a held channel's beta fixed at 0, its k and s
  # (not in the objective then) fixed at the start.
  is_fixed <- rep(FALSE, length(pn)); names(is_fixed) <- pn
  for (h in held) {
    c <- match(h, ch)
    is_fixed[c(1 + c, 1 + nc + nz + c, 1 + 2 * nc + nz + c)] <- TRUE
  }
  fixed <- start; for (h in held) fixed[[1 + match(h, ch)]] <- 0
  free <- pn[!is_fixed]
  full <- function(pf) { v <- fixed; v[free] <- pf; v }

  # nlsLM through its formula interface on the reduced problem: one data
  # frame row per residual, an indicator column carrying the prior's
  # pseudo-residual row.
  safe <- make.names(pn, unique = TRUE)
  dat <- data.frame(y = c(y, if (pidx > 0) sqrt(prior$precision) * prior$mean), ind = c(rep(0, n), if (pidx > 0) 1))
  for (c in seq_len(nc)) dat[[paste0("A", c)]] <- c(a[[c]], if (pidx > 0) 1)
  for (j in seq_len(nz)) dat[[paste0("Z", j)]] <- c(z[[j]], if (pidx > 0) 0)
  pv <- function(i) if (is_fixed[[i]]) sprintf("(%.17g)", fixed[[i]]) else safe[i]
  terms <- c(pv(1))
  for (c in seq_len(nc)) {
    if (pn[1 + c] %in% held) next
    terms <- c(terms, sprintf("%s / (1 + (%s / A%d)^%s)", pv(1 + c), pv(1 + nc + nz + c), c, pv(1 + 2 * nc + nz + c)))
  }
  for (j in seq_len(nz)) terms <- c(terms, sprintf("%s * Z%d", pv(1 + nc + j), j))
  rhs <- sprintf("(1 - ind) * (%s)", paste(terms, collapse = " + "))
  if (pidx > 0) rhs <- sprintf("%s + ind * %.17g * %s", rhs, sqrt(prior$precision), pv(1 + pidx))
  form <- as.formula(paste("y ~", rhs))
  fstart <- setNames(as.list(start[free]), safe[!is_fixed])
  fit <- nlsLM(form, data = dat, start = fstart, lower = lower[free], upper = upper[free], control = mh_tight)

  an <- nls.lm(par = start[free], lower = lower[free], upper = upper[free],
               fn = function(pf) fn(full(pf)),
               jac = function(pf) jac(full(pf))[, free, drop = FALSE], control = mh_tight)
  mh_near(coef(fit), an$par, 1e-7, paste(name, "nlsLM vs analytic nls.lm"), floor = 1)
  pf <- an$par
  for (it in 1:8) {
    J <- jac(full(pf))[, free, drop = FALSE]
    pf <- pf - solve(crossprod(J), crossprod(J, fn(full(pf))))[, 1]
  }
  mh_near(an$par, pf, 1e-7, paste(name, "MINPACK x vs the polished stationary point"), floor = 1)
  if (any(pf <= lower[free] | pf >= upper[free])) stop("self-check failed: ", name, " a free parameter reached a bound")
  x <- full(pf)
  r <- fn(x)
  J <- jac(x)
  g <- as.numeric(crossprod(J[, free, drop = FALSE], r))
  gnorm <- sqrt(colSums(J[, free, drop = FALSE]^2)) * sqrt(sum(r^2))
  if (any(abs(g) > 1e-9 * gnorm)) stop("self-check failed: ", name, " stationarity on free parameters, max ", max(abs(g) / gnorm))

  # KKT on each held beta, over the channel's whole (k, s) box.
  for (h in held) {
    c <- match(h, ch)
    rr <- r[seq_len(n)]
    for (kk in exp(seq(log(lower[[1 + nc + nz + c]]), log(upper[[1 + nc + nz + c]]), length.out = 41))) {
      for (ss in seq(0.5, 5, length.out = 37)) {
        gb <- sum(rr * mh_hill(a[[c]], kk, ss))
        if (!(gb > 0)) stop("self-check failed: ", name, " held ", h, " gradient ", gb, " at k = ", kk, ", s = ", ss)
      }
    }
  }

  ssr <- sum((pred(x) - y)^2)
  tss <- sum((y - mean(y))^2)
  df <- n - length(pn)
  s2 <- ssr / df
  cov <- s2 * solve(crossprod(J[, free, drop = FALSE]))
  se <- sqrt(diag(cov))
  if (length(held) == 0 && pidx == 0) {
    mh_near(se, summary(fit)$coefficients[, "Std. Error"], 1e-6, paste(name, "std errors vs summary(nlsLM)"))
  }

  cols <- list(y = mh_vec(y))
  for (c in ch) cols[[c]] <- mh_vec(a[[c]])
  for (j in ct) cols[[j]] <- mh_vec(z[[j]])
  doc <- list(
    name = name,
    n = n,
    channels = as.list(ch),
    controls = as.list(ct),
    columns = cols,
    coefficients = as.list(pn),
    start = mh_vec(start),
    lower = lapply(lower, function(v) if (is.infinite(v)) if (v > 0) "Inf" else "-Inf" else num(v)),
    upper = lapply(upper, function(v) if (is.infinite(v)) if (v > 0) "Inf" else "-Inf" else num(v)),
    estimates = mh_vec(x),
    free = as.list(free),
    std_errors = mh_vec(se),
    held = as.list(held),
    deviance = num(ssr),
    r2 = num(1 - ssr / tss),
    residual_std_err = num(sqrt(s2)),
    df = df,
    r_nlslm = mh_vec(coef(fit)),
    r_nlslm_deviance = num(deviance(fit))
  )
  names(doc$lower) <- NULL; names(doc$upper) <- NULL
  if (pidx > 0) doc$prior <- list(channel = prior$channel, mean = num(prior$mean), precision = num(prior$precision))
  mh_cases[[length(mh_cases) + 1]] <<- doc
}

# Adstocked spend: a weekly spend with dark weeks, run through a
# geometric carry-over (the column REG_MMM_HILL expects is already
# adstocked), rounded to 4 decimals.
mh_spend <- function(n, level, dark, decay) {
  x <- round(rgamma(n, shape = 1, scale = level), 4)
  x[sample.int(n, dark)] <- 0
  out <- numeric(n); carry <- 0
  for (i in seq_len(n)) { carry <- x[i] + decay * carry; out[i] <- carry }
  round(out, 4)
}

set.seed(20261102)

# One channel, no control, a concave-to-S curve.
n1 <- 104
a1 <- mh_spend(n1, 40, 8, 0.5)
y1 <- round(50 + 30 * mh_hill(a1, 1.2 * mean(a1), 1.8) + rnorm(n1, 0, 1.5), 4)
mh_case("one_channel", y1, list(tv = a1), list())

# Two channels and a price control.
n2 <- 156
tv2 <- mh_spend(n2, 50, 10, 0.3)
se2 <- mh_spend(n2, 20, 20, 0.2)
pr2 <- round(10 + 0.8 * sin(seq_len(n2) / 9) + rnorm(n2, 0, 0.3), 4)
y2 <- round(100 + 40 * mh_hill(tv2, 0.9 * mean(tv2), 1.4) + 25 * mh_hill(se2, 1.5 * mean(se2), 2.6) -
              3 * pr2 + rnorm(n2, 0, 1), 4)
mh_case("two_channels_control", y2, list(tv = tv2, search = se2), list(price = pr2))

# The same rows under a Gaussian MAP prior on the search beta.
mh_case("prior_on_channel", y2, list(tv = tv2, search = se2), list(price = pr2),
        prior = list(channel = "search", mean = 18, precision = 0.25))

# A held channel: display carries a small negative effect, so its beta
# is held at 0 under nonnegative.
n4 <- 120
tv4 <- mh_spend(n4, 30, 6, 0.5)
di4 <- mh_spend(n4, 15, 12, 0.3)
y4 <- round(20 + 18 * mh_hill(tv4, mean(tv4), 1.6) - 2.5 * mh_hill(di4, mean(di4), 1.2) + rnorm(n4, 0, 1), 4)
mh_case("held_channel", y4, list(tv = tv4, display = di4), list(), held = "display")

write_golden("mmm_hill", list(
  generator = "scripts/reference/gen_mmm_hill.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    minpack.lm = as.character(packageVersion("minpack.lm")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "REG_MMM_HILL (bounded Hill media-mix least squares)",
  r_function = "minpack.lm::nlsLM (bounds, same start) = minpack.lm::nls.lm (analytic Jacobian) to 1e-7, Gauss-Newton polished to the stationary point",
  note = "estimates in coefficient-table order (k in the channel's units); std_errors over free in its order; held channels' beta is 0 and their k, s are not identified",
  cases = mh_cases
))
