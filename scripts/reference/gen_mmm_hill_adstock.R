# gen_mmm_hill_adstock.R — R oracle for REG_MMM_HILL's in-model adstock
# (params.adstock, U71 E2-S2 / E2-S3, #551): one case per kernel —
# geometric, delayed, weibull_cdf, weibull_pdf — the joint Hill +
# adstock least-squares fit pinned to minpack.lm on the same objective,
# the same bounds and the same start.
#
# Writes <out_dir>/mmm_hill_adstock.json (default
# internal/regmmm/testdata). Standalone, NOT sourced by gen_reference.R,
# so regenerating it moves no other golden:
#
#   make reference-mmm-hill-adstock
#
# (the target appends the `// golden-hash:` footer). CI never runs R:
# the golden is committed. Pins R 4.6.1, minpack.lm 1.2-4 and jsonlite
# 2.0.0. Seeded RNG; every input is rounded to 4 decimals and stored, so
# the Go test fits the very rows R fitted.
#
# The model (internal/regmmm/hill.go + hill_adstock.go), one channel and
# one control per case, rows in time order:
#
#   a_t = sum_{l=0..min(t, L)} w_l(phi) * x_{t-l}     (unnormalised)
#   y_t = b0 + beta * hill(a_t, k, s) + gamma * z_t
#   hill(a, k, s) = 1 / (1 + (k / a)^s)              (0 at a = 0)
#
# with the kernel weights written here independently of Pulse:
#   geometric    w_l = decay^l
#   delayed      w_l = decay^((l - peak)^2)
#   weibull_cdf  w_l = pweibull(l, shape, scale, lower.tail = FALSE)
#   weibull_pdf  w_l = dweibull(l + 1, shape, scale)
# minimising 1/2 * sum r^2 with beta >= 0, k in [0.02, 20] * kref, s in
# [0.5, 5], and the kernel's box: geometric decay [0, 0.99]; delayed
# decay [0.01, 0.99], peak [0, L]; Weibull shape [0.1, 10], scale
# [0.1, L + 1]. kref is the channel's adstock mean at the reference
# start (decay 0.5; a Weibull kernel at shape 1, scale -1/log 0.5).
# Pulse searches k and s on the log scale; R searches them directly —
# the same objective, bounds and optimum.
#
# The start is Pulse's scan: for each decay d in {0.1, 0.3, 0.5, 0.7,
# 0.9} the kernel start at d (geometric decay d; delayed (d, min(1, L));
# Weibull (1, clamp(-1/log d, 0.1, L + 1))), k = kref, s = 1 and
# [b0, beta, gamma] by least squares on the Hill column there (a beta
# <= 0 starts at 1e-3); the d with the smallest SSR wins (first on a
# tie).
#
# Self-checks before anything is written (gen_mmm_hill.R's pattern):
#   * nlsLM (a formula calling the model, finite-difference Jacobian,
#     bounds) and nls.lm (the same MINPACK solver on this script's
#     analytic Jacobian) agree to 1e-6 relative;
#   * Gauss-Newton polishing on the analytic Jacobian moves the nls.lm x
#     by under 1e-6 relative; the POLISHED point is stored, its gradient
#     J'r is 0 to 1e-9 of |J_j| |r| on every parameter, and no parameter
#     sits on a bound;
#   * the stored standard errors equal summary(nlsLM)'s (its
#     finite-difference J) to 1e-4.
#
# Figures, as REG_MMM_HILL defines them:
#   estimates        (intercept), channel, control, k:<c>, s:<c>, then
#                    the kernel's parameters (decay:, peak:, shape:,
#                    scale:<c>)
#   std_errors       sqrt(diag(sigma^2 (J'J)^-1)) at the polished x, in
#                    the parameters' own units, sigma^2 = SSR / (n - p)
#   deviance         SSR;  r2 = 1 - SSR / TSS;  residual_std_err =
#                    sqrt(SSR / (n - p));  df = n - p.

suppressPackageStartupMessages({
  library(jsonlite)
  library(minpack.lm)
})

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "internal/regmmm/testdata"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_mmm_hill_adstock.R pins R 4.6.1, got ", R.version.string)
ma_pins <- c(minpack.lm = "1.2.4", jsonlite = "2.0.0")
for (ma_p in names(ma_pins)) {
  if (as.character(packageVersion(ma_p)) != ma_pins[[ma_p]]) stop("gen_mmm_hill_adstock.R pins ", ma_p, " ", ma_pins[[ma_p]], ", found ", packageVersion(ma_p))
}

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
vec <- function(v) lapply(unname(as.numeric(v)), num)
near <- function(a, b, tol, what, floor = 0) {
  a <- as.numeric(a); b <- as.numeric(b)
  scale <- pmax(abs(a), abs(b), floor)
  ok <- abs(a - b) <= tol * scale | scale == 0
  if (!all(ok)) stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / scale))
}
hill <- function(x, k, s) ifelse(x > 0, 1 / (1 + (k / x)^s), 0)
tight <- nls.lm.control(ftol = 1e-15, ptol = 1e-15, gtol = 0, maxiter = 1000, maxfev = 100000)

# Kernels: names, weights over lags 0..L, analytic d w / d phi (one row
# per parameter), boxes and the start at decay d.
kernels <- list(
  geometric = list(
    params = "decay",
    w = function(phi, L) phi[1]^(0:L),
    dw = function(phi, L) { l <- 0:L; rbind(ifelse(l == 0, 0, l * phi[1]^(l - 1))) },
    lower = function(L) 0, upper = function(L) 0.99,
    start = function(d, L) d
  ),
  delayed = list(
    params = c("decay", "peak"),
    w = function(phi, L) phi[1]^(((0:L) - phi[2])^2),
    dw = function(phi, L) {
      l <- 0:L; q <- (l - phi[2])^2; w <- phi[1]^q
      rbind(q * w / phi[1], -2 * (l - phi[2]) * log(phi[1]) * w)
    },
    lower = function(L) c(0.01, 0), upper = function(L) c(0.99, L),
    start = function(d, L) c(d, min(1, L))
  ),
  weibull_cdf = list(
    params = c("shape", "scale"),
    w = function(phi, L) pweibull(0:L, shape = phi[1], scale = phi[2], lower.tail = FALSE),
    dw = function(phi, L) {
      l <- 0:L; k <- phi[1]; lam <- phi[2]
      w <- exp(-(l / lam)^k); u <- (l / lam)^k
      lr <- ifelse(l > 0, log(l / lam), 0)
      rbind(-w * u * lr, w * u * k / lam)
    },
    lower = function(L) c(0.1, 0.1), upper = function(L) c(10, L + 1),
    start = function(d, L) c(1, min(max(-1 / log(d), 0.1), L + 1))
  ),
  weibull_pdf = list(
    params = c("shape", "scale"),
    w = function(phi, L) dweibull((0:L) + 1, shape = phi[1], scale = phi[2]),
    dw = function(phi, L) {
      l <- 0:L; k <- phi[1]; lam <- phi[2]
      z <- (l + 1) / lam; w <- dweibull(l + 1, shape = k, scale = lam)
      rbind(w * (1 / k + log(z) * (1 - z^k)), w * k * (z^k - 1) / lam)
    },
    lower = function(L) c(0.1, 0.1), upper = function(L) c(10, L + 1),
    start = function(d, L) c(1, min(max(-1 / log(d), 0.1), L + 1))
  )
)

# conv is a_t = sum_{l=0..min(t, L)} w_l x_{t-l}.
conv <- function(x, w) {
  n <- length(x); out <- numeric(n)
  for (t in seq_len(n)) {
    l <- 0:min(t - 1, length(w) - 1)
    out[t] <- sum(w[l + 1] * x[t - l])
  }
  out
}

ma_cases <- list()

# ma_case fits one kernel case: spend x (raw), control z, target y.
ma_case <- function(name, kernel, L, y, x, z) {
  K <- kernels[[kernel]]
  n <- length(y); np <- length(K$params)
  pn <- c("(intercept)", "tv", "price", "k:tv", "s:tv", paste0(K$params, ":tv"))
  kref <- mean(conv(x, K$w(K$start(0.5, L), L)))
  lower <- c(-Inf, 0, -Inf, 0.02 * kref, 0.5, K$lower(L))
  upper <- c(Inf, Inf, Inf, 20 * kref, 5, K$upper(L))
  names(lower) <- names(upper) <- pn

  pred <- function(p) {
    a <- conv(x, K$w(p[5 + seq_len(np)], L))
    p[[1]] + p[[2]] * hill(a, p[[4]], p[[5]]) + p[[3]] * z
  }
  fn <- function(p) pred(p) - y
  jac <- function(p) {
    phi <- p[5 + seq_len(np)]
    a <- conv(x, K$w(phi, L))
    dw <- K$dw(phi, L)
    b <- p[[2]]; k <- p[[4]]; s <- p[[5]]
    h <- hill(a, k, s); hh <- h * (1 - h)
    J <- matrix(0, n, length(pn), dimnames = list(NULL, pn))
    J[, 1] <- 1
    J[, 2] <- h
    J[, 3] <- z
    J[, 4] <- -b * s / k * hh
    J[, 5] <- b * ifelse(a > 0, log(a / k), 0) * hh
    dhda <- ifelse(a > 0, b * s * hh / a, 0)
    for (i in seq_len(np)) J[, 5 + i] <- dhda * conv(x, dw[i, ])
    J
  }

  # Pulse's start scan.
  best <- NULL; best_ssr <- Inf
  for (d in c(0.1, 0.3, 0.5, 0.7, 0.9)) {
    phi <- K$start(d, L)
    a <- conv(x, K$w(phi, L))
    X0 <- cbind(1, hill(a, kref, 1), z)
    lin <- solve(crossprod(X0), crossprod(X0, y))[, 1]
    if (!(lin[[2]] > 0)) lin[[2]] <- 1e-3
    p0 <- c(lin, kref, 1, phi)
    ssr <- sum(fn(p0)^2)
    if (ssr < best_ssr) { best <- p0; best_ssr <- ssr }
  }
  start <- best; names(start) <- pn

  # nlsLM through a formula that calls the model: finite-difference J.
  safe <- make.names(pn, unique = TRUE)
  dat <- data.frame(y = y)
  model_f <- function(...) pred(c(...))
  form <- as.formula(paste("y ~ model_f(", paste(safe, collapse = ", "), ")"))
  fit <- nlsLM(form, data = dat, start = setNames(as.list(start), safe),
               lower = unname(lower), upper = unname(upper), control = tight)

  an <- nls.lm(par = start, lower = lower, upper = upper, fn = fn, jac = jac, control = tight)
  near(coef(fit), an$par, 1e-6, paste(name, "nlsLM vs analytic nls.lm"), floor = 1)
  pf <- an$par
  for (it in 1:8) {
    J <- jac(pf)
    pf <- pf - solve(crossprod(J), crossprod(J, fn(pf)))[, 1]
  }
  near(an$par, pf, 1e-6, paste(name, "MINPACK x vs the polished stationary point"), floor = 1)
  if (any(pf <= lower | pf >= upper)) stop("self-check failed: ", name, " a parameter reached a bound")
  r <- fn(pf); J <- jac(pf)
  g <- as.numeric(crossprod(J, r))
  gnorm <- sqrt(colSums(J^2)) * sqrt(sum(r^2))
  if (any(abs(g) > 1e-9 * gnorm)) stop("self-check failed: ", name, " stationarity, max ", max(abs(g) / gnorm))

  ssr <- sum(r^2)
  tss <- sum((y - mean(y))^2)
  df <- n - length(pn)
  s2 <- ssr / df
  se <- sqrt(diag(s2 * solve(crossprod(J))))
  near(se, summary(fit)$coefficients[, "Std. Error"], 1e-4, paste(name, "std errors vs summary(nlsLM)"))

  ma_cases[[length(ma_cases) + 1]] <<- list(
    name = name,
    kernel = kernel,
    max_lag = L,
    n = n,
    columns = list(y = vec(y), tv = vec(x), price = vec(z)),
    coefficients = as.list(pn),
    kref = num(kref),
    start = vec(start),
    estimates = vec(pf),
    std_errors = vec(se),
    deviance = num(ssr),
    r2 = num(1 - ssr / tss),
    residual_std_err = num(sqrt(s2)),
    df = df,
    r_nlslm = vec(coef(fit)),
    r_nlslm_deviance = num(deviance(fit))
  )
}

# Raw weekly spend with dark weeks, rounded to 4 decimals.
spend <- function(n, level, dark) {
  x <- round(rgamma(n, shape = 1.5, scale = level / 1.5), 4)
  x[sample.int(n, dark)] <- 0
  x
}

set.seed(20261115)
n <- 104
truth <- list(
  geometric = list(L = 8, phi = 0.6, k = 1.1, s = 1.8),
  delayed = list(L = 6, phi = c(0.5, 1.6), k = 1.0, s = 2.0),
  weibull_cdf = list(L = 8, phi = c(2, 3), k = 1.2, s = 1.6),
  weibull_pdf = list(L = 8, phi = c(2.5, 3), k = 0.9, s = 2.2)
)
for (kn in names(truth)) {
  tr <- truth[[kn]]
  x <- spend(n, 40, 10)
  z <- round(10 + 0.8 * sin(seq_len(n) / 9) + rnorm(n, 0, 0.3), 4)
  a <- conv(x, kernels[[kn]]$w(tr$phi, tr$L))
  y <- round(60 + 30 * hill(a, tr$k * mean(a), tr$s) - 3 * z + rnorm(n, 0, 1), 4)
  ma_case(kn, kn, tr$L, y, x, z)
}

doc <- list(
  generator = "scripts/reference/gen_mmm_hill_adstock.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    minpack.lm = as.character(packageVersion("minpack.lm")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "REG_MMM_HILL params.adstock (joint Hill + adstock bounded least squares, one case per kernel)",
  r_function = "minpack.lm::nlsLM (finite-difference J, bounds, same start) = minpack.lm::nls.lm (analytic Jacobian) to 1e-6, Gauss-Newton polished to the stationary point; kernels from stats::pweibull / dweibull and closed forms",
  note = "estimates and std_errors in coefficient-table order (k and the kernel parameters in their own units)",
  cases = ma_cases
)
path <- file.path(out_dir, "mmm_hill_adstock.json")
cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
message("wrote ", path, " (", length(ma_cases), " cases)")
