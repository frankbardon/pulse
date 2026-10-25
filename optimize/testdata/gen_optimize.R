# gen_optimize.R — R oracles for the public optimize package
# (mmm-estimation E1-S3):
#
#   least_squares.json  optimize.LeastSquares  vs  minpack.lm::nlsLM
#   minimize.json       optimize.Minimize      vs  nloptr NLOPT_LD_SLSQP
#
# Run via `make reference-optimize` (which appends the `// golden-hash:`
# footer TestOptimize_OracleGoldensHashed checks), or on its own:
#
#   Rscript optimize/testdata/gen_optimize.R optimize/testdata
#
# CI never runs R: the JSON is committed. Inputs are fixed (a seeded RNG,
# rounded to 4 decimals, stored), every number is written with
# sprintf("%.17g") so the Go tests read back R's doubles, and a rerun is
# byte-identical.
#
# Pinned packages: minpack.lm 1.2-4 (MINPACK lmder / lmdif, Fortran-free
# C, but its install links gfortran: on Homebrew R use the Fortran
# workaround of docs/src/internals/regenerating-goldens.md, The R
# toolchain), nloptr 2.2.1, jsonlite 2.0.0.
#
# Self-checks before anything is written (the oracle must be the optimum,
# not merely R's stopping point):
#   * least squares: the stored fit is minpack.lm::nls.lm on the analytic
#     Jacobian; nlsLM (formula interface, finite-difference Jacobian, the
#     same MINPACK solver) agrees with it to 1e-7 relative — its numeric
#     Jacobian pins x to only ~3e-8. MINPACK stops on ptol a little short
#     of the stationary point, so Gauss-Newton polishing must move x by
#     under 1e-7 relative, and the gradient J'r is 0 to 1e-7 of
#     |J_j| |r| on every free parameter;
#   * a bound-binding case: minpack.lm holds a bound by clamping the
#     whole step, which stalls short of the bound-constrained optimum
#     (about 5% off in a and k here, and start-dependent). The oracle is
#     nlsLM on the REDUCED problem (the held parameter fixed at its
#     bound), valid because the held parameter's cost gradient pushes
#     outward (KKT); the clamped run's x and cost are kept as evidence,
#     and its cost must not beat the oracle's;
#   * minimize: the KKT system (gradient = multipliers x active-constraint
#     gradients, on the eq, the binding ineq and the active bounds) is
#     solved by least squares and its residual is 0 to 1e-9 relative; the
#     ineq multiplier is >= 0; every constraint holds to 1e-12. nloptr
#     reports no multipliers, so the stored ones are these KKT solves.
#
# Conventions written into the JSON (they are Pulse's):
#   * cost = 1/2 * sum(r^2) (nlsLM's deviance() is sum(r^2));
#   * an ineq constraint is Fn(x) >= 0 (nloptr's eval_g_ineq is <= 0, so
#     the script hands it -Fn); multipliers satisfy
#     grad f = sum(lambda_i grad Fn_i) on the free variables.

suppressPackageStartupMessages({
  library(jsonlite)
  library(minpack.lm)
  library(nloptr)
})

# Refuse any R or package but the pinned ones: the stored figures are
# theirs.
pins <- c(minpack.lm = "1.2.4", nloptr = "2.2.1", jsonlite = "2.0.0")
if (R.version.string != "R version 4.6.1 (2026-06-24)") stop("gen_optimize.R pins R 4.6.1, running ", R.version.string)
for (p in names(pins)) {
  if (as.character(packageVersion(p)) != pins[[p]]) stop("gen_optimize.R pins ", p, " ", pins[[p]], ", found ", packageVersion(p))
}

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "optimize/testdata"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
vec <- function(v) lapply(unname(as.numeric(v)), num)
# A bound vector: JSON has no infinity, so an open side is the string
# "Inf" / "-Inf", which the Go loader reads back as an open bound.
bvec <- function(v) lapply(unname(as.numeric(v)), function(x) {
  if (is.infinite(x)) if (x > 0) "Inf" else "-Inf" else num(x)
})
near <- function(a, b, tol, what, floor = 0) {
  a <- as.numeric(a); b <- as.numeric(b)
  scale <- pmax(abs(a), abs(b), floor)
  ok <- abs(a - b) <= tol * scale | scale == 0
  if (!all(ok)) stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / scale))
}
write_golden <- function(name, doc) {
  path <- file.path(out_dir, paste0(name, ".json"))
  txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
  # The golden-hash footer (appended by the Makefile) brings its own newline.
  cat(txt, file = path)
  message("wrote ", path, " (", length(doc$cases), " cases)")
}
meta <- function(primitive, r_function, packages, note) {
  list(
    generator = "optimize/testdata/gen_optimize.R",
    r_version = R.version.string,
    packages = packages,
    primitive = primitive,
    r_function = r_function,
    note = note
  )
}

lm_tight <- nls.lm.control(ftol = 1e-15, ptol = 1e-15, gtol = 0, maxiter = 1000, maxfev = 100000)

# ------------------------------------------------------------ least squares
ls_cases <- list()

# ls_case: model in {"hill", "exp_decay"}; t, y the data; start, lower,
# upper the problem; held names the parameters expected on a bound.
ls_case <- function(name, model, t, y, start, lower, upper, held) {
  pn <- names(start)
  if (model == "hill") {
    form <- y ~ b0 + beta * t^s / (t^s + h^s)
    fn <- function(p) p[["b0"]] + p[["beta"]] * t^p[["s"]] / (t^p[["s"]] + p[["h"]]^p[["s"]]) - y
    jac <- function(p) {
      b0 <- p[["b0"]]; beta <- p[["beta"]]; h <- p[["h"]]; s <- p[["s"]]
      ts <- t^s; hs <- h^s; d <- ts + hs
      cbind(b0 = 1, beta = ts / d,
            h = -beta * ts * s * h^(s - 1) / d^2,
            s = beta * ts * hs * (log(t) - log(h)) / d^2)
    }
  } else {
    form <- y ~ a * exp(-k * t) + c
    fn <- function(p) p[["a"]] * exp(-p[["k"]] * t) + p[["c"]] - y
    jac <- function(p) cbind(a = exp(-p[["k"]] * t), k = -p[["a"]] * t * exp(-p[["k"]] * t), c = 1)
  }
  dat <- data.frame(t = t, y = y)
  is_held <- pn %in% held
  free <- pn[!is_held]
  # Held parameters sit on their lower bound (the only binding kind used).
  fixed <- setNames(lower[is_held], held)
  full <- function(p) { v <- setNames(numeric(length(pn)), pn); v[free] <- p; v[held] <- fixed; v }

  # nlsLM on the stated (bounded) problem.
  fit <- nlsLM(form, data = dat, start = as.list(start), lower = lower, upper = upper, control = lm_tight)
  bounded_x <- coef(fit)[pn]
  if (length(held) == 0) {
    rform <- form
    rfit <- fit
  } else {
    # minpack.lm holds a bound by clamping the full step, which stalls
    # short of the bound-constrained optimum (here ~5% off in a and k,
    # start-dependent). The oracle is therefore nlsLM on the REDUCED
    # problem — the held parameters fixed at their bounds, the rest
    # unbounded — which is the bound-constrained optimum when the KKT
    # sign check below holds. The clamped run is kept as evidence.
    rform <- form
    for (nm in held) {
      rform <- as.formula(gsub(paste0("\\b", nm, "\\b"), sprintf("%.17g", fixed[[nm]]), deparse(rform)))
    }
    rfit <- nlsLM(rform, data = dat, start = as.list(start[free]), control = lm_tight)
  }
  # The stored fit: the same MINPACK solver on the analytic Jacobian.
  # nlsLM's formula interface differentiates numerically, which pins x
  # to ~3e-8 relative; the two must agree to 1e-7.
  an <- nls.lm(par = start[free], lower = lower[!is_held], upper = upper[!is_held],
               fn = function(p) fn(full(p)),
               jac = function(p) jac(full(p))[, free, drop = FALSE], control = lm_tight)
  x <- full(an$par[free])
  near(coef(rfit)[free], x[free], 1e-7, paste(name, "nlsLM vs analytic nls.lm"), floor = 1)
  # MINPACK stops on ptol a little short of the stationary point
  # (normalised gradient ~1e-8); Gauss-Newton polishing on the free
  # parameters reaches it to rounding, and the stored x must be within
  # 1e-7 of it.
  p <- x[free]
  for (k in 1:6) {
    J <- jac(full(p))[, free, drop = FALSE]
    p <- p - solve(crossprod(J), crossprod(J, fn(full(p))))[, 1]
  }
  near(x[free], p, 1e-7, paste(name, "MINPACK x vs the polished stationary point"), floor = 1)
  if (any(x[free] <= lower[!is_held] | x[free] >= upper[!is_held])) stop("self-check failed: ", name, " a free parameter reached a bound")

  r <- fn(x)
  J <- jac(x)[, pn, drop = FALSE]
  g <- as.numeric(crossprod(J, r))
  gnorm <- sqrt(colSums(J^2)) * sqrt(sum(r^2))
  if (any(abs(g[!is_held]) > 1e-7 * gnorm[!is_held])) stop("self-check failed: ", name, " stationarity on free parameters")
  # KKT on a lower-held parameter: the cost gradient is > 0, i.e. the
  # unconstrained optimum lies below the bound.
  if (any(g[is_held] <= 1e-6 * gnorm[is_held])) stop("self-check failed: ", name, " held parameter does not push on its bound")

  extra <- list()
  if (length(held) == 0) {
    near(bounded_x, x, 1e-7, paste(name, "bounded nlsLM vs analytic nls.lm"), floor = 1)
    # sigma^2 (J'J)^-1 on m - n df at the analytic J, cross-checked
    # against summary(nlsLM)'s (finite-difference J) to 1e-6.
    s2 <- sum(r^2) / (length(r) - length(pn))
    sev <- sqrt(diag(s2 * solve(crossprod(J))))
    near(sev, summary(fit)$coefficients[pn, "Std. Error"], 1e-6, paste(name, "std errors vs summary(nlsLM)"))
    extra$std_errors <- vec(sev)
  } else {
    if (deviance(fit) / 2 < sum(r^2) / 2) stop("self-check failed: ", name, " the clamped nlsLM run beat the reduced optimum")
    extra$nlslm_clamped_x <- vec(bounded_x)
    extra$nlslm_clamped_cost <- num(deviance(fit) / 2)
  }

  rows <- lapply(seq_along(t), function(i) vec(c(t[i], y[i])))
  ls_cases[[length(ls_cases) + 1]] <<- c(list(
    name = name,
    model = model,
    params = as.list(pn),
    rows = rows,
    start = vec(start),
    lower = bvec(lower),
    upper = bvec(upper),
    x = vec(x),
    cost = num(sum(r^2) / 2),
    held = as.list(held)
  ), extra)
}

set.seed(20261025)
n <- 40
t <- round(seq(0.5, 20, length.out = n), 4)
y <- round(1.2 + 6 * t^1.8 / (t^1.8 + 5^1.8) + rnorm(n, 0, 0.15), 4)
ls_case("hill_interior", "hill", t, y,
        start = c(b0 = 0.5, beta = 3, h = 2, s = 1),
        lower = c(0, 0, 0.1, 0.5), upper = c(10, 50, 100, 5), held = character(0))

t2 <- round(seq(0, 10, length.out = 30), 4)
y2 <- round(4 * exp(-0.45 * t2) - 0.3 + rnorm(length(t2), 0, 0.05), 4)
ls_case("exp_decay_bound_binding", "exp_decay", t2, y2,
        start = c(a = 1, k = 1, c = 1),
        lower = c(0, 0, 0), upper = c(Inf, Inf, Inf), held = "c")

write_golden("least_squares", c(
  meta("optimize.LeastSquares (bounded nonlinear least squares)",
       "minpack.lm::nls.lm(lower, upper, analytic jac; ftol = ptol = 1e-15), cross-checked by minpack.lm::nlsLM to 1e-7",
       list(minpack.lm = as.character(packageVersion("minpack.lm")),
            jsonlite = as.character(packageVersion("jsonlite"))),
       "cost = 1/2 sum r^2; held = parameters on a bound; std_errors = sqrt(diag(sigma^2 (J'J)^-1)) on m - n df at the analytic J (= summary(nlsLM) to 1e-6), interior cases only; upper \"Inf\" = open"),
  list(cases = ls_cases)))

# ----------------------------------------------------------------- minimize
mn_cases <- list()

# One convex problem: a strictly convex objective (quadratic plus an
# exponential), one linear equality, one concave Fn >= 0 inequality
# (a convex feasible disc) and box bounds — a unique optimum where the
# equality, the inequality and one lower bound all bind.
ctr <- c(1, 2, 1.5, -1)
f <- function(x) sum((x - ctr)^2) + exp(0.5 * (x[1] - x[2]))
gf <- function(x) {
  e <- 0.5 * exp(0.5 * (x[1] - x[2]))
  2 * (x - ctr) + c(e, -e, 0, 0)
}
heq <- function(x) sum(x) - 2.5
gheq <- function(x) c(1, 1, 1, 1)
fin <- function(x) 2.5 - x[2]^2 - x[3]^2
gfin <- function(x) c(0, -2 * x[2], -2 * x[3], 0)
lo <- c(0, 0, 0, 0)
hi <- c(3, 3, 3, 3)
x0 <- c(1, 1, 1, 0.5)

sol <- nloptr(
  x0 = x0,
  eval_f = function(x) list(objective = f(x), gradient = gf(x)),
  lb = lo, ub = hi,
  eval_g_eq = function(x) list(constraints = heq(x), jacobian = matrix(gheq(x), 1)),
  eval_g_ineq = function(x) list(constraints = -fin(x), jacobian = matrix(-gfin(x), 1)),
  opts = list(algorithm = "NLOPT_LD_SLSQP", xtol_rel = 1e-15, ftol_rel = 1e-16, maxeval = 10000,
              tol_constraints_eq = 1e-14, tol_constraints_ineq = 1e-14)
)
if (sol$status < 0) stop("nloptr failed: ", sol$message)
x <- sol$solution
if (abs(heq(x)) > 1e-12 || fin(x) < -1e-12) stop("self-check failed: minimize feasibility")
if (fin(x) > 1e-9) stop("self-check failed: the inequality was meant to bind")
on_lo <- abs(x - lo) <= 1e-12
on_hi <- abs(x - hi) <= 1e-12
if (!identical(which(on_lo | on_hi), 4L)) stop("self-check failed: expected only x4 on a bound")
# KKT: grad f = l_eq grad h + mu grad Fn + sum nu_j e_j (active bounds).
A <- cbind(gheq(x), gfin(x), diag(4)[, on_lo | on_hi, drop = FALSE])
kkt <- qr.solve(A, gf(x))
res <- gf(x) - as.numeric(A %*% kkt)
if (max(abs(res)) > 1e-9 * max(abs(gf(x)))) stop("self-check failed: KKT residual ", max(abs(res)))
if (kkt[2] < 0) stop("self-check failed: negative inequality multiplier")
# The lower-bound multiplier must push up into the box (grad f >= 0 there).
if (kkt[3] < 0) stop("self-check failed: lower-bound multiplier sign")

mn_cases[[1]] <- list(
  name = "convex_eq_ineq_bounds",
  objective = "sum((x - c)^2) + exp(0.5 (x1 - x2)), c = (1, 2, 1.5, -1)",
  constraints = list(
    list(type = "eq", fn = "x1 + x2 + x3 + x4 - 2.5"),
    list(type = "ineq", fn = "2.5 - x2^2 - x3^2")
  ),
  x0 = vec(x0),
  lower = vec(lo),
  upper = vec(hi),
  x = vec(x),
  f = num(sol$objective),
  multipliers = vec(kkt[1:2]),
  active_lower = as.list(which(on_lo) - 1L),
  active_upper = as.list(which(on_hi) - 1L),
  nloptr_status = sol$status,
  nloptr_iterations = sol$iterations
)

write_golden("minimize", c(
  meta("optimize.Minimize (SLSQP, equality + inequality + bounds)",
       "nloptr::nloptr(algorithm = NLOPT_LD_SLSQP, xtol_rel = 1e-15, ftol_rel = 1e-16); multipliers from the KKT system at the solution",
       list(nloptr = as.character(packageVersion("nloptr")),
            jsonlite = as.character(packageVersion("jsonlite"))),
       "ineq is Fn(x) >= 0; multipliers in constraint order with grad f = sum(lambda_i grad Fn_i) on the free variables; active_* are 0-based variable indices"),
  list(cases = mn_cases)))
