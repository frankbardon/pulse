#!/usr/bin/env Rscript
# gen_reference.R — R oracle for Pulse's shared statistical primitives.
#
# Writes one JSON golden per distribution family into the output
# directory (default internal/processing/testdata/reference). Every
# number is emitted with sprintf("%.17g"), which round-trips a double
# exactly, so the Go oracle tests read back the very doubles R used.
#
# Run via `make reference` (which also appends the `// golden-hash:`
# footer TestGoldensNotHandEdited checks). CI never runs R: the goldens
# are committed and read-only there.
#
# Requirements: base R + jsonlite.
#
# Grid choices worth knowing:
#   * Inputs are filtered to reference values >= 1e-300 (the smallest
#     comfortably-normal double) so the far tail is exercised wherever it
#     is representable.
#   * Student-t df stops at 1e5: R's pt()/qt() switch to a normal
#     approximation above df = 4e5, which is not an oracle.
#   * Student-t quantiles are qt() POLISHED by Newton steps on
#     log pt(): in the far tail qt() disagrees with R's own pt() (by
#     1.5% in p at df = 1.5, p = 1e-300; ~2e-8 at df = 3), while pt()
#     (TOMS 708) is accurate there. The raw qt() value is kept as
#     r_qt for reference.
#   * The studentized range reference is NOT ptukey()/qtukey(). R's
#     ptukey() is accurate to only ~1e-8 relative for df >= 10 (worse at
#     small df: 2e-5 at df = 2, k = 2, where the closed form
#     P(|T| > q/sqrt(2)) is exact) and computes the upper tail as
#     1 - CDF; qtukey() is documented to ~4 decimal places. The oracle is a
#     nested stats::integrate() of the survival function written in a
#     cancellation-free form; ptukey()/qtukey() are recorded alongside as
#     an independent cross-check.

suppressPackageStartupMessages(library(jsonlite))

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}

meta <- function(primitive, r_fn, note) {
  list(
    generator = "scripts/reference/gen_reference.R",
    r_version = R.version.string,
    packages = list(
      stats = as.character(packageVersion("stats")),
      jsonlite = as.character(packageVersion("jsonlite"))
    ),
    primitive = primitive,
    r_function = r_fn,
    note = note
  )
}

write_golden <- function(name, doc) {
  path <- file.path(out_dir, paste0(name, ".json"))
  txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
  # writeLines would add a trailing newline; the golden-hash footer
  # (appended by `make reference`) starts with its own newline.
  cat(txt, file = path)
  message("wrote ", path, " (", length(doc$cases), " cases)")
}

keep <- function(v) is.finite(v) && v >= 1e-300

# ---------------------------------------------------------------- Student t
t_df <- c(1, 1.5, 2, 3, 4.7, 5, 7.25, 10, 23.37, 30, 100, 1000, 1e4, 1e5)
t_stat <- c(1e-3, 0.1, 0.5, 1, 1.5, 1.96, 2.5, 3, 4, 6, 8, 12, 20, 30, 50,
            100, 300, 1e3, 1e4, 1e6, 1e9, 1e12, 1e15)

t_cases <- list()
for (df in t_df) for (t in t_stat) {
  p2 <- 2 * pt(-t, df)
  if (!keep(p2)) next
  t_cases[[length(t_cases) + 1]] <- list(
    t = num(t), df = num(df),
    p_two_sided = num(p2),
    cdf_neg = num(pt(-t, df)),
    cdf_pos = num(pt(t, df))
  )
}
write_golden("student_t_p", c(meta(
  "studentTTwoSidedP / studentTCDF",
  "2*pt(-|t|, df); pt(-t, df); pt(t, df)",
  "cdf_neg is the lower tail at -t, cdf_pos the CDF at +t."
), list(cases = t_cases)))

tq_p <- c(1e-300, 1e-200, 1e-100, 1e-50, 1e-20, 1e-12, 1e-8, 1e-5, 1e-3, 0.01,
          0.025, 0.05, 0.1, 0.25, 0.4, 0.49, 0.51, 0.6, 0.75, 0.9, 0.95,
          0.975, 0.99, 0.999, 1 - 1e-5, 1 - 1e-8)
# Newton-polish a qt() quantile so pt(q, df, lower.tail) == p to
# working precision. Steps on the log probability, whose derivative is
# +/- dt/pt.
polish_qt <- function(p, df, lower.tail = TRUE) {
  q <- qt(p, df, lower.tail = lower.tail)
  if (!is.finite(q) || q == 0) return(q)
  for (i in 1:100) {
    lp <- pt(q, df, lower.tail = lower.tail, log.p = TRUE)
    slope <- exp(dt(q, df, log = TRUE) - lp)
    if (!lower.tail) slope <- -slope
    qn <- q - (lp - log(p)) / slope
    if (!is.finite(qn) || sign(qn) != sign(q)) qn <- q / 2 + qn / 2
    done <- abs(qn - q) <= 4 * .Machine$double.eps * abs(qn)
    q <- qn
    if (done) break
  }
  resid <- pt(q, df, lower.tail = lower.tail, log.p = TRUE) - log(p)
  # The residual is in log p, which is ~ -690 at p = 1e-300; allow a few
  # ulps of that magnitude (relative error in p stays below 1e-11).
  if (abs(resid) > 1e-14 * max(1, abs(log(p)))) stop(sprintf("qt polish failed: p=%g df=%g resid=%g", p, df, resid))
  q
}

tq_cases <- list()
for (df in t_df) for (p in tq_p) {
  q <- polish_qt(p, df)
  if (!is.finite(q)) next
  # The lower tail quantile magnitude grows like p^(-1/df); keep it
  # representable and well inside the double range.
  if (abs(q) > 1e290) next
  tq_cases[[length(tq_cases) + 1]] <- list(
    p = num(p), df = num(df), q = num(q), r_qt = num(qt(p, df))
  )
}
write_golden("student_t_quantile", c(meta(
  "studentTQuantile",
  "qt(p, df) polished by Newton on log pt()",
  "Lower-tail quantile: P(T <= q) = p; r_qt is the unpolished qt()."
), list(cases = tq_cases)))

alpha_grid <- c(0.9, 0.5, 0.2, 0.1, 0.05, 0.01, 0.001, 1e-5, 1e-8, 1e-12, 1e-20)
ti_cases <- list()
for (df in t_df) for (a in alpha_grid) {
  q <- polish_qt(a / 2, df, lower.tail = FALSE)
  if (!is.finite(q) || q > 1e290) next
  ti_cases[[length(ti_cases) + 1]] <- list(
    alpha = num(a), df = num(df), q = num(q),
    r_qt = num(qt(a / 2, df, lower.tail = FALSE))
  )
}
write_golden("student_t_inverse_two_sided", c(meta(
  "studentTInverseTwoSided",
  "qt(alpha/2, df, lower.tail = FALSE) polished by Newton on log pt()",
  "Two-sided critical value: P(|T| >= q) = alpha; r_qt is the unpolished qt()."
), list(cases = ti_cases)))

# ------------------------------------------------------------- chi-square
chi_df <- c(0.5, 1, 2, 3, 4.5, 5, 10, 17.3, 30, 100, 1000, 1e4, 1e5)
chi_mult <- c(1e-3, 0.01, 0.1, 0.5, 0.9, 1, 1.1, 1.5, 2, 3, 5, 10, 30)
chi_abs <- c(1e-6, 0.01, 1, 10, 50, 100, 300, 600, 1000, 1350)
chi_cases <- list()
for (df in chi_df) for (x in sort(unique(c(df * chi_mult, chi_abs)))) {
  p <- pchisq(x, df, lower.tail = FALSE)
  if (!keep(p)) next
  chi_cases[[length(chi_cases) + 1]] <- list(x = num(x), df = num(df), p = num(p))
}
write_golden("chi_square_survival", c(meta(
  "chiSquareSurvival",
  "pchisq(x, df, lower.tail = FALSE)",
  "Upper-tail probability P(X >= x)."
), list(cases = chi_cases)))

# ---------------------------------------------------------------------- F
f_df1 <- c(1, 2, 3, 5, 10, 30, 100, 1000)
f_df2 <- c(1, 2, 5, 10, 17.3, 30, 100, 1000, 1e5)
f_stat <- c(1e-3, 0.1, 0.5, 1, 2, 4, 10, 50, 1e3, 1e6, 1e10)
f_cases <- list()
for (d1 in f_df1) for (d2 in f_df2) for (f in f_stat) {
  p <- pf(f, d1, d2, lower.tail = FALSE)
  if (!keep(p)) next
  f_cases[[length(f_cases) + 1]] <- list(f = num(f), df1 = num(d1), df2 = num(d2), p = num(p))
}
write_golden("f_survival", c(meta(
  "fSurvival",
  "pf(f, df1, df2, lower.tail = FALSE)",
  "Upper-tail probability P(F >= f)."
), list(cases = f_cases)))

# ----------------------------------------------------------------- normal
z_grid <- c(-37.5, -37, -30, -20, -12, -10, -8, -6, -5, -4, -3, -2.5, -1.96,
            -1.5, -1, -0.5, -0.1, -1e-3, 0, 1e-3, 0.1, 0.5, 1, 1.5, 1.96, 2.5,
            3, 4, 5, 6, 8)
n_cases <- list()
for (z in z_grid) {
  p <- pnorm(z)
  if (!keep(p)) next
  n_cases[[length(n_cases) + 1]] <- list(z = num(z), cdf = num(p))
}
write_golden("normal_cdf", c(meta(
  "standardNormalCDF",
  "pnorm(z)",
  "Lower-tail CDF Phi(z)."
), list(cases = n_cases)))

np_grid <- c(1e-300, 1e-200, 1e-100, 1e-50, 1e-20, 1e-10, 1e-5, 1e-3, 0.01,
             0.02, 0.02425, 0.025, 0.05, 0.1, 0.3, 0.45, 0.5, 0.55, 0.7, 0.9,
             0.95, 0.975, 0.97575, 0.98, 0.99, 0.999, 1 - 1e-5, 1 - 1e-10,
             1 - 1e-15)
np_cases <- list()
for (p in np_grid) {
  np_cases[[length(np_cases) + 1]] <- list(p = num(p), q = num(qnorm(p)))
}
write_golden("normal_ppf", c(meta(
  "standardNormalPPF",
  "qnorm(p)",
  "Inverse CDF; q = 0 at p = 0.5 is compared with an absolute floor."
), list(cases = np_cases)))

# ------------------------------------------------------------- Kolmogorov
ks_lambda <- c(0.15, 0.2, 0.25, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1, 1.1,
               1.2, 1.36, 1.5, 1.63, 2, 2.5, 3, 4, 5, 7, 10, 12, 15, 18.5)
ks_cases <- list()
for (l in ks_lambda) {
  # Limiting distribution of sqrt(n) * D_n (two-sided); an essentially
  # zero tolerance runs the series until its terms underflow.
  p <- .Call(stats:::C_pkolmogorov_two_limit, l, FALSE, 1e-300)
  if (!keep(p)) next
  ks_cases[[length(ks_cases) + 1]] <- list(lambda = num(l), p = num(p))
}
write_golden("kolmogorov_survival", c(meta(
  "kolmogorovSurvival",
  "stats:::C_pkolmogorov_two_limit(lambda, lower.tail = FALSE, tol = 1e-300)",
  "Q_KS(lambda) = 2 * sum_{j>=1} (-1)^(j-1) exp(-2 j^2 lambda^2), the limiting Kolmogorov survival."
), list(cases = ks_cases)))

# ------------------------------------------------------ studentized range
# Survival of the range of k iid N(0,1), written without cancellation:
#   P(R > t) = k * int phi(z) * ( a^(k-1) - (a - c)^(k-1) ) dz
#   a = P(Z > z), c = P(Z > z + t)
#          = -k * int phi(z) a^(k-1) expm1((k-1) log1p(-c/a)) dz
range_surv <- function(t, k) {
  sapply(t, function(tt) {
    if (tt <= 0) return(1)
    f <- function(z) {
      la <- pnorm(z, lower.tail = FALSE, log.p = TRUE)
      lc <- pnorm(z + tt, lower.tail = FALSE, log.p = TRUE)
      v <- -k * exp(dnorm(z, log = TRUE) + (k - 1) * la) *
        expm1((k - 1) * log1p(-exp(lc - la)))
      v[!is.finite(v)] <- 0
      v
    }
    mid <- -tt / 2
    integ(f, -Inf, mid) + integ(f, mid, Inf)
  })
}

# integrate() with a 1e-13 relative target. A segment whose integrand
# has underflowed to ~0 (the far tail of the outer integral) trips
# integrate()'s divergence heuristic under abs.tol = 0; the retry gives
# it an absolute floor 1e-16 below the mass accumulated so far, which
# cannot move the result.
integ <- function(f, lo, hi, floor_abs = 0) {
  r <- tryCatch(
    integrate(f, lo, hi, rel.tol = 1e-13, abs.tol = 0, subdivisions = 5000L)$value,
    error = function(e) NULL
  )
  if (is.null(r)) {
    r <- integrate(f, lo, hi, rel.tol = 1e-13, abs.tol = max(floor_abs, 1e-320),
                   subdivisions = 5000L)$value
  }
  r
}

tukey_surv <- function(q, k, df) {
  lconst <- log(2) + (df / 2) * log(df / 2) - lgamma(df / 2)
  g <- function(s) {
    out <- numeric(length(s))
    pos <- s > 0
    lh <- lconst + (df - 1) * log(s[pos]) - df * s[pos]^2 / 2
    out[pos] <- exp(lh) * range_surv(q * s[pos], k)
    out
  }
  sig <- 1 / sqrt(2 * df)
  brk <- sort(unique(c(0, pmax(0, 1 + sig * c(-40, -20, -10, -5, -2, 0, 2, 5, 10, 20, 40)))))
  total <- 0
  for (i in seq_len(length(brk) - 1)) {
    total <- total + integ(g, brk[i], brk[i + 1], 1e-16 * total)
  }
  total + integ(g, brk[length(brk)], Inf, 1e-16 * total)
}

tk_k <- c(2, 3, 5, 10, 20)
tk_df <- c(2, 5, 10, 30, 120, 1000, 1e4)
tk_q <- c(0.5, 1, 2, 3, 4, 6, 9, 15)
tk_cases <- list()
for (k in tk_k) for (df in tk_df) for (q in tk_q) {
  p <- tukey_surv(q, k, df)
  if (!keep(p)) next
  tk_cases[[length(tk_cases) + 1]] <- list(
    q = num(q), k = num(k), df = num(df), p = num(p),
    r_ptukey_upper = num(ptukey(q, k, df, lower.tail = FALSE))
  )
}
write_golden("studentized_range_survival", c(meta(
  "studentizedRangeSurvival",
  "nested integrate() of the cancellation-free survival; ptukey(q, k, df, lower.tail = FALSE) as cross-check",
  "p is the oracle (relative accuracy ~1e-12); r_ptukey_upper is R's ptukey, accurate to ~1e-8."
), list(cases = tk_cases)))

tki_alpha <- c(0.2, 0.1, 0.05, 0.01, 0.001)
tki_df <- c(2, 5, 10, 30, 120, 1000)
tki_cases <- list()
for (k in tk_k) for (df in tki_df) for (a in tki_alpha) {
  r_q <- qtukey(a, k, df, lower.tail = FALSE)
  # Bracket around R's qtukey (accurate to ~1e-4, worse at df = 2) and
  # refine on the log of the oracle survival.
  fn <- function(q) log(tukey_surv(q, k, df)) - log(a)
  root <- uniroot(fn, c(r_q * 0.9, r_q * 1.1), extendInt = "downX",
                  tol = 1e-14 * r_q, maxiter = 200)$root
  tki_cases[[length(tki_cases) + 1]] <- list(
    alpha = num(a), k = num(k), df = num(df), q = num(root),
    r_qtukey = num(r_q)
  )
}
write_golden("studentized_range_inverse", c(meta(
  "studentizedRangeInverse",
  "uniroot() on the oracle survival; qtukey(alpha, k, df, lower.tail = FALSE) as cross-check",
  "q solves P(Q > q) = alpha; r_qtukey is R's qtukey, accurate to ~1e-4."
), list(cases = tki_cases)))
