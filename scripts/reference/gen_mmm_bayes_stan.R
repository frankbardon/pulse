# gen_mmm_bayes_stan.R — Stan oracle for REG_MMM_BAYES (U70 E1-S2, #562;
# E1-S3, #563; E1-S4, #564): posterior means of the Bayesian media-mix
# model in every cell of the adstock kernel x saturation curve matrix
# (4 x 3), and with the lift-test likelihood (params.lift_tests) on two
# cells, sampled by CmdStan from the model written independently in
# scripts/reference/mmm_bayes.stan.
#
# Writes <out_dir>/mmm_bayes_stan.json. Run by `make reference-stan`,
# which appends the `// golden-hash:` footer the Go test checks:
#
#   Rscript scripts/reference/gen_mmm_bayes_stan.R internal/regmmm/testdata
#
# CI never runs R or Stan: the golden is committed. Pins R 4.6.1,
# cmdstanr 0.9.0 (stan-dev r-universe), CmdStan 2.40.0, posterior 1.7.1
# and jsonlite 2.0.0, and refuses any other version. Install:
#
#   Rscript -e 'install.packages("cmdstanr", repos = c("https://stan-dev.r-universe.dev", getOption("repos")))'
#   Rscript -e 'cmdstanr::install_cmdstan(version = "2.40.0")'
#
# Each case is one market's weekly series simulated here (seeded, every
# value rounded to 4 decimals and stored), so the Go test
# (internal/regmmm/bayes_stan_oracle_heavy_test.go) fits the very rows
# Stan sampled, through REG_MMM_BAYES with the case's stored params
# (default priors). The data are scaled exactly as Pulse scales them
# (max|y|, the channel's peak spend, centred and max-scaled controls),
# and the Stan program's priors are Pulse's defaults on that scale, so
# the two posteriors are the same distribution: their means agree within
# Monte Carlo error. Stan's figure carries its own MCSE (posterior's
# mcse_mean); the Go test adds Pulse's (sd / sqrt(ESS)) and allows a
# stated number of combined MCSEs.
#
# Self-checks before anything is written: every reported quantity's R-hat
# below 1.01 and bulk ESS above 2000, and no more than 0.5% divergent
# transitions — a Stan run that did not mix is no oracle.
#
# A lift case (`lift` in its row) adds one lift test on tv: rows
# start..end (week = row), spend_delta = frac x the stored window spend
# (spread in proportion to the spend, as Pulse spreads it), and a
# measured lift = the TRUE model's window-only change in sales plus
# Normal(0, se) noise drawn after the series (so the series is the
# lift-free case's, bit for bit, at the same seed). Stan reads the
# changed spend and the lift; the Go test reads params.lift_tests.
#
# Extending: add a kernel or curve to mmm_bayes.stan (data `kernel`,
# `curve`) and a row to `cases` below; the Go test reads every case
# generically (its params, rows and expected means). Each case's truth
# saturates clearly inside its own spend range (a curve the data barely
# bend leaves the curve parameter weakly identified, which Stan reports
# as divergences and the self-checks refuse).

suppressPackageStartupMessages({
  library(jsonlite)
  library(cmdstanr)
  library(posterior)
})

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "internal/regmmm/testdata"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_mmm_bayes_stan.R pins R 4.6.1, got ", R.version.string)
pins <- c(cmdstanr = "0.9.0", posterior = "1.7.1", jsonlite = "2.0.0")
for (p in names(pins)) {
  if (as.character(packageVersion(p)) != pins[[p]]) stop("gen_mmm_bayes_stan.R pins ", p, " ", pins[[p]], ", found ", packageVersion(p))
}
if (cmdstan_version() != "2.40.0") stop("gen_mmm_bayes_stan.R pins CmdStan 2.40.0, found ", cmdstan_version())

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
r4 <- function(x) round(x, 4)

stan_file <- file.path(dirname(sub("^--file=", "", grep("^--file=", commandArgs(FALSE), value = TRUE)[1])), "mmm_bayes.stan")
if (!file.exists(stan_file)) stan_file <- "scripts/reference/mmm_bayes.stan"
mod <- cmdstan_model(stan_file, dir = tempdir(), quiet = TRUE) # the binary stays out of the tree

kernel_code <- c(geometric = 0L, delayed = 1L, weibull_cdf = 2L, weibull_pdf = 3L)
curve_code <- c(hill = 0L, logistic = 1L, michaelis_menten = 2L)

# kernel_weights is lag l's raw weight (normalize false), the kernel's
# closed form (distlagparams).
kernel_weights <- function(kernel, phi, L) {
  l <- 0:L
  switch(kernel,
    geometric = phi$decay^l,
    delayed = phi$decay^((l - phi$peak)^2),
    weibull_cdf = exp(-(l / phi$scale)^phi$shape),
    weibull_pdf = dweibull(l + 1, shape = phi$shape, scale = phi$scale))
}

adstock <- function(x, w) {
  n <- length(x); L <- length(w) - 1
  vapply(seq_len(n), function(t) sum(w[1:min(t, L + 1)] * x[t - 0:min(t - 1, L)]), 0)
}

# curve_value is the saturation curve at raw adstock a and the curve's
# raw parameters q (k, s; lam; k), 0 at a = 0.
curve_value <- function(curve, a, q) {
  switch(curve,
    hill = ifelse(a > 0, 1 / (1 + (q$k / a)^q$s), 0),
    logistic = (1 - exp(-q$lam * a)) / (1 + exp(-q$lam * a)),
    michaelis_menten = a / (q$k + a))
}

# simulate is one case's series: bursty spend (dark weeks), a price
# control, the kernel's adstock, the case's curve and Gaussian noise.
simulate <- function(seed, n, kernel, phi, L, curve, q, beta) {
  set.seed(seed)
  spend <- ifelse(runif(n) < 0.65, 20 + 150 * runif(n), 0)
  price <- 2 * (runif(n) - 0.5)
  a <- adstock(spend, kernel_weights(kernel, phi, L))
  h <- curve_value(curve, a, q)
  y <- 200 + beta * h - 5 * price + rnorm(n, 0, 1.5)
  list(week = seq_len(n), tv = r4(spend), price = r4(price), sales = r4(y))
}

# The truths: each kernel's adstock reaches about 2x (geometric,
# delayed), 3x (weibull_cdf) and 1x (weibull_pdf) the peak spend, so each
# curve's half-saturation sits inside that range.
cases <- list(
  list(name = "geometric_hill", kernel = "geometric", curve = "hill", seed = 7001, n = 104, L = 8,
       phi = list(decay = 0.5), q = list(k = 120, s = 1.5), beta = 40),
  list(name = "delayed_hill", kernel = "delayed", curve = "hill", seed = 7002, n = 104, L = 8,
       phi = list(decay = 0.5, peak = 2), q = list(k = 120, s = 1.5), beta = 40),
  list(name = "weibull_cdf_hill", kernel = "weibull_cdf", curve = "hill", seed = 7003, n = 104, L = 8,
       phi = list(shape = 1.5, scale = 2.5), q = list(k = 150, s = 1.5), beta = 40),
  list(name = "weibull_pdf_hill", kernel = "weibull_pdf", curve = "hill", seed = 7004, n = 104, L = 8,
       phi = list(shape = 2, scale = 3), q = list(k = 15, s = 2), beta = 60),
  list(name = "geometric_logistic", kernel = "geometric", curve = "logistic", seed = 7011, n = 104, L = 8,
       phi = list(decay = 0.5), q = list(lam = 0.012), beta = 40),
  list(name = "delayed_logistic", kernel = "delayed", curve = "logistic", seed = 7012, n = 104, L = 8,
       phi = list(decay = 0.5, peak = 2), q = list(lam = 0.012), beta = 40),
  list(name = "weibull_cdf_logistic", kernel = "weibull_cdf", curve = "logistic", seed = 7013, n = 104, L = 8,
       phi = list(shape = 1.5, scale = 2.5), q = list(lam = 0.008), beta = 40),
  list(name = "weibull_pdf_logistic", kernel = "weibull_pdf", curve = "logistic", seed = 7014, n = 104, L = 8,
       phi = list(shape = 2, scale = 3), q = list(lam = 0.03), beta = 60),
  list(name = "geometric_michaelis_menten", kernel = "geometric", curve = "michaelis_menten", seed = 7021, n = 104, L = 8,
       phi = list(decay = 0.5), q = list(k = 60), beta = 50),
  list(name = "delayed_michaelis_menten", kernel = "delayed", curve = "michaelis_menten", seed = 7022, n = 104, L = 8,
       phi = list(decay = 0.5, peak = 2), q = list(k = 60), beta = 50),
  list(name = "weibull_cdf_michaelis_menten", kernel = "weibull_cdf", curve = "michaelis_menten", seed = 7023, n = 104, L = 8,
       phi = list(shape = 1.5, scale = 2.5), q = list(k = 100), beta = 50),
  list(name = "weibull_pdf_michaelis_menten", kernel = "weibull_pdf", curve = "michaelis_menten", seed = 7024, n = 104, L = 8,
       phi = list(shape = 2, scale = 3), q = list(k = 25), beta = 60),
  # The lift-test likelihood: a switch-off on the default cell and a +50%
  # test on a two-parameter kernel with a one-parameter curve.
  list(name = "geometric_hill_lift", kernel = "geometric", curve = "hill", seed = 7001, n = 104, L = 8,
       phi = list(decay = 0.5), q = list(k = 120, s = 1.5), beta = 40,
       lift = list(start = 41L, end = 48L, frac = -1, se = 4)),
  list(name = "delayed_logistic_lift", kernel = "delayed", curve = "logistic", seed = 7012, n = 104, L = 8,
       phi = list(decay = 0.5, peak = 2), q = list(lam = 0.012), beta = 40,
       lift = list(start = 61L, end = 66L, frac = 0.5, se = 3))
)

# The coefficient names Pulse reports (REG_MMM_BAYES's table) mapped to
# the Stan quantity whose posterior mean is the same figure.
coef_map <- function(kernel, curve) {
  m <- list("(intercept)" = "intercept_raw", "tv" = "beta_raw[1]", "price" = "gamma_raw[1]", "sigma" = "sigma_raw")
  if (curve %in% c("hill", "michaelis_menten")) m[["k:tv"]] <- "k_raw[1]"
  if (curve == "hill") m[["s:tv"]] <- "s[1]"
  if (curve == "logistic") m[["lam:tv"]] <- "lam_raw[1]"
  if (kernel %in% c("geometric", "delayed")) m[["decay:tv"]] <- "decay[1]"
  if (kernel == "delayed") m[["peak:tv"]] <- "peak[1]"
  if (kernel %in% c("weibull_cdf", "weibull_pdf")) {
    m[["shape:tv"]] <- "wshape[1]"
    m[["scale:tv"]] <- "wscale[1]"
  }
  m
}

# A second argument (comma-separated case names) runs only those cases
# and writes them alone: for developing a case, never for the golden.
if (length(args) >= 2) cases <- Filter(function(cs) cs$name %in% strsplit(args[[2]], ",")[[1]], cases)

# lift_spend is x with frac x the window spend spread over start..end in
# proportion to the spend (bayes_lift.go's liftSpend; frac = -1 is
# exactly zero spend there).
lift_spend <- function(x, start, end, delta) {
  win <- sum(x[start:end])
  out <- x
  out[start:end] <- x[start:end] * max(0, 1 + delta / win)
  out
}

out_cases <- list()
for (cs in cases) {
  d <- simulate(cs$seed, cs$n, cs$kernel, cs$phi, cs$L, cs$curve, cs$q, cs$beta)
  lift <- NULL
  if (!is.null(cs$lift)) {
    lf <- cs$lift
    delta <- lf$frac * sum(d$tv[lf$start:lf$end])
    changed <- lift_spend(d$tv, lf$start, lf$end, delta)
    w <- kernel_weights(cs$kernel, cs$phi, cs$L)
    win <- lf$start:lf$end
    truth <- cs$beta * sum(curve_value(cs$curve, adstock(changed, w), cs$q)[win] - curve_value(cs$curve, adstock(d$tv, w), cs$q)[win])
    lift <- list(start = lf$start, end = lf$end, spend_delta = delta, se = lf$se, spend = changed,
                 truth = truth, obs = r4(truth + rnorm(1, 0, lf$se)))
  }
  ymax <- max(abs(d$sales))
  xmax <- max(d$tv)
  zmean <- mean(d$price)
  zscale <- max(abs(d$price - zmean))
  data <- list(N = cs$n, C = 1L, J = 1L, L = cs$L, kernel = kernel_code[[cs$kernel]], curve = curve_code[[cs$curve]],
               ys = d$sales / ymax, spend = list(d$tv), xscale = array(xmax), share = array(sum(d$tv) / ymax),
               zs = list((d$price - zmean) / zscale), yscale = ymax, zmean = array(zmean), zscale = array(zscale),
               T = 0L, lift_c = integer(0), lift_from = integer(0), lift_to = integer(0),
               lift_spend = matrix(numeric(0), nrow = 0, ncol = cs$n), lift_obs = numeric(0), lift_se = numeric(0))
  if (!is.null(lift)) {
    data$T <- 1L
    data$lift_c <- array(1L)
    data$lift_from <- array(lift$start)
    data$lift_to <- array(lift$end)
    data$lift_spend <- list(lift$spend)
    data$lift_obs <- array(lift$obs)
    data$lift_se <- array(lift$se)
  }
  fit <- mod$sample(data = data, seed = cs$seed, chains = 4, parallel_chains = 4, iter_warmup = 2000,
                    iter_sampling = 10000, adapt_delta = 0.95, refresh = 0, show_messages = FALSE)
  m <- coef_map(cs$kernel, cs$curve)
  vars <- c(unlist(m), "roi[1]")
  sm <- summarise_draws(fit$draws(variables = vars), mean, sd, mcse_mean, rhat, ess_bulk)
  if (any(sm$rhat > 1.01)) stop("self-check failed: ", cs$name, " R-hat ", max(sm$rhat))
  if (any(sm$ess_bulk < 2000)) stop("self-check failed: ", cs$name, " bulk ESS ", min(sm$ess_bulk))
  div <- sum(fit$diagnostic_summary(quiet = TRUE)$num_divergent)
  if (div > 0.005 * 4 * 10000) stop("self-check failed: ", cs$name, " ", div, " divergences")
  row <- function(v) {
    r <- sm[sm$variable == v, ]
    list(mean = num(r$mean), sd = num(r$sd), mcse = num(r$mcse_mean))
  }
  coefs <- list()
  for (nm in names(m)) coefs[[nm]] <- row(m[[nm]])
  params <- list(channels = list("tv"), time = "week", adstock = cs$kernel, max_lag = cs$L,
                 chains = 4L, warmup = 1000L, draws = 2500L)
  if (cs$curve != "hill") params$saturation <- cs$curve
  if (!is.null(lift)) {
    params$lift_tests <- list(list(channel = "tv", start = lift$start, end = lift$end,
                                   spend_delta = num(lift$spend_delta), lift = num(lift$obs), se = num(lift$se)))
  }
  message(sprintf("%s: %d divergences, min ESS %.0f", cs$name, div, min(sm$ess_bulk)))
  out_cases[[length(out_cases) + 1]] <- list(
    name = cs$name, kernel = cs$kernel, curve = cs$curve, rng_seed = cs$seed, params = params,
    truth = c(lapply(cs$phi, num), lapply(cs$q, num), list(beta = num(cs$beta)),
              if (is.null(lift)) list() else list(lift = num(lift$truth))),
    rows = list(week = d$week, tv = lapply(d$tv, num), price = lapply(d$price, num), sales = lapply(d$sales, num)),
    stan = list(chains = 4L, warmup = 2000L, draws = 10000L, adapt_delta = 0.95, seed = cs$seed, divergences = div),
    coefficients = coefs, roi = row("roi[1]"))
}

doc <- list(
  generator = "scripts/reference/gen_mmm_bayes_stan.R (make reference-stan)",
  stan_program = "scripts/reference/mmm_bayes.stan",
  r_version = R.version.string,
  packages = list(cmdstanr = as.character(packageVersion("cmdstanr")), cmdstan = cmdstan_version(),
                  posterior = as.character(packageVersion("posterior")), jsonlite = as.character(packageVersion("jsonlite"))),
  note = paste(
    "Posterior means (and SD, MCSE) of REG_MMM_BAYES's coefficient table in original units and of each channel's ROI,",
    "sampled by CmdStan from an independent Stan program of the same model and default priors on Pulse's scaled data."),
  cases = out_cases)
path <- file.path(out_dir, "mmm_bayes_stan.json")
cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
message("wrote ", path, " (", length(out_cases), " cases)")
