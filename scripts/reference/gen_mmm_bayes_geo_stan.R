# gen_mmm_bayes_geo_stan.R — Stan oracle for REG_MMM_BAYES's
# geo-hierarchical model (params.geo; U70 E2-S3): posterior means of the
# partially pooled media-mix model on a three-geo, two-channel panel,
# curves shared across the geos and pooled per geo (params.per_geo_curves),
# sampled by CmdStan from the model written independently in
# scripts/reference/mmm_bayes_geo.stan.
#
# Writes <out_dir>/mmm_bayes_geo_stan.json. Run by `make
# reference-stan-geo`, which appends the `// golden-hash:` footer the Go
# test checks:
#
#   Rscript scripts/reference/gen_mmm_bayes_geo_stan.R internal/regmmm/testdata
#
# CI never runs R or Stan: the golden is committed. Pins R 4.6.1,
# cmdstanr 0.9.0, CmdStan 2.40.0, posterior 1.7.1 and jsonlite 2.0.0
# (gen_mmm_bayes_stan.R's toolchain) and refuses any other version.
#
# Each case is one panel simulated here (seeded, every value rounded to
# 4 decimals and stored): per geo 104 weeks of two channels' bursty
# spend (tv, search), a price control, geometric adstock, the Hill curve
# and Gaussian noise, every geo with its own baseline, each channel's own
# decay and beta per geo, and in the per-geo-curve case its own Hill k
# and s per geo — so every pooling scale tau has a clearly non-zero
# truth. The Go test (internal/regmmm/bayes_geo_stan_oracle_heavy_test.go)
# fits the very rows Stan sampled through REG_MMM_BAYES with the case's
# stored params (default priors); the data are scaled exactly as Pulse
# scales a panel (max|y| over the panel, each channel's panel peak spend,
# panel-centred and max-scaled controls), so the two posteriors are one
# distribution and their means agree within Monte Carlo error. It
# compares every coefficient (each geo's intercept and beta, the
# population beta, the control, population and per-geo decay, the curve,
# sigma, every tau), the population ROI mu and each channel's all-geo and
# per-geo ROI.
#
# Self-checks before anything is written: every reported quantity's R-hat
# below 1.01 and bulk ESS above 2000, and no more than 0.5% divergent
# transitions — a Stan run that did not mix is no oracle.

suppressPackageStartupMessages({
  library(jsonlite)
  library(cmdstanr)
  library(posterior)
})

args <- commandArgs(trailingOnly = TRUE)
out_dir <- if (length(args) >= 1) args[[1]] else "internal/regmmm/testdata"
dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)

if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_mmm_bayes_geo_stan.R pins R 4.6.1, got ", R.version.string)
pins <- c(cmdstanr = "0.9.0", posterior = "1.7.1", jsonlite = "2.0.0")
for (p in names(pins)) {
  if (as.character(packageVersion(p)) != pins[[p]]) stop("gen_mmm_bayes_geo_stan.R pins ", p, " ", pins[[p]], ", found ", packageVersion(p))
}
if (cmdstan_version() != "2.40.0") stop("gen_mmm_bayes_geo_stan.R pins CmdStan 2.40.0, found ", cmdstan_version())

num <- function(x) {
  if (length(x) != 1) stop("num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
r4 <- function(x) round(x, 4)

stan_file <- file.path(dirname(sub("^--file=", "", grep("^--file=", commandArgs(FALSE), value = TRUE)[1])), "mmm_bayes_geo.stan")
if (!file.exists(stan_file)) stan_file <- "scripts/reference/mmm_bayes_geo.stan"
mod <- cmdstan_model(stan_file, dir = tempdir(), quiet = TRUE) # the binary stays out of the tree

adstock <- function(x, decay, L) {
  w <- decay^(0:L)
  vapply(seq_along(x), function(t) sum(w[1:min(t, L + 1)] * x[t - 0:min(t - 1, L)]), 0)
}
hill <- function(a, k, s) ifelse(a > 0, 1 / (1 + (k / a)^s), 0)

G <- 3L
N <- 104L
L <- 8L
channels <- c("tv", "search")

# simulate is one case's panel. truth$<channel> holds per geo decay,
# beta, k (raw adstock units) and s.
simulate <- function(seed, truth) {
  set.seed(seed)
  rows <- list(geo = c(), week = c(), tv = c(), search = c(), price = c(), sales = c())
  roi <- matrix(0, G, 2)
  for (g in 1:G) {
    tv <- ifelse(runif(N) < 0.7, 30 + 200 * runif(N), 0)
    sr <- ifelse(runif(N) < 0.6, 10 + 90 * runif(N), 0)
    price <- 2 * (runif(N) - 0.5)
    sp <- list(tv = tv, search = sr)
    y <- truth$base[g] - 5 * price
    for (ci in 1:2) {
      tr <- truth[[channels[ci]]]
      eff <- tr$beta[g] * hill(adstock(sp[[ci]], tr$decay[g], L), tr$k[g], tr$s[g])
      y <- y + eff
      roi[g, ci] <- sum(eff) / sum(sp[[ci]])
    }
    y <- y + rnorm(N, 0, 4)
    rows$geo <- c(rows$geo, rep(g, N))
    rows$week <- c(rows$week, seq_len(N))
    rows$tv <- c(rows$tv, r4(tv))
    rows$search <- c(rows$search, r4(sr))
    rows$price <- c(rows$price, r4(price))
    rows$sales <- c(rows$sales, r4(y))
  }
  list(rows = rows, roi = roi)
}

# The truths: each geo's baseline, per channel each geo's decay and beta
# (tv decay 0.35 - 0.65, search 0.15 - 0.45 — logit spreads about 0.6 —
# and betas about 1.5x apart), the Hill curve saturating inside each
# channel's adstock range; in the per-geo-curve case k and s vary by geo
# too.
base <- c(200, 260, 320)
shared <- list(base = base,
  tv = list(decay = c(0.35, 0.5, 0.65), beta = c(60, 75, 95), k = rep(150, 3), s = rep(1.5, 3)),
  search = list(decay = c(0.15, 0.3, 0.45), beta = c(25, 35, 45), k = rep(60, 3), s = rep(1.2, 3)))
per_geo <- list(base = base,
  tv = list(decay = c(0.35, 0.5, 0.65), beta = c(60, 75, 95), k = c(110, 150, 210), s = c(1.2, 1.5, 1.9)),
  search = list(decay = c(0.15, 0.3, 0.45), beta = c(25, 35, 45), k = c(40, 60, 85), s = c(1.0, 1.3, 1.6)))
cases <- list(
  list(name = "geo_shared_curves", seed = 7101, per_geo_curves = FALSE, truth = shared),
  list(name = "geo_per_geo_curves", seed = 7102, per_geo_curves = TRUE, truth = per_geo))

# A second argument (comma-separated case names) runs only those cases
# and writes them alone: for developing a case, never for the golden.
if (length(args) >= 2) cases <- Filter(function(cs) cs$name %in% strsplit(args[[2]], ",")[[1]], cases)

# coef_map is REG_MMM_BAYES's geo coefficient table (bayes_geo.go
# geoCoefNames) mapped to the Stan quantity whose posterior mean is the
# same figure.
coef_map <- function(per_geo_curves) {
  m <- list()
  for (g in 1:G) m[[sprintf("(intercept)[%d]", g)]] <- sprintf("intercept_raw[%d]", g)
  for (ci in 1:2) {
    ch <- channels[ci]
    m[[ch]] <- sprintf("beta_pop_raw[%d]", ci)
    for (g in 1:G) m[[sprintf("%s[%d]", ch, g)]] <- sprintf("beta_raw[%d,%d]", g, ci)
    m[[paste0("decay:", ch)]] <- sprintf("mu_decay[%d]", ci)
    for (g in 1:G) m[[sprintf("decay:%s[%d]", ch, g)]] <- sprintf("decay[%d,%d]", ci, g)
    m[[paste0("k:", ch)]] <- sprintf("mu_k_raw[%d]", ci)
    m[[paste0("s:", ch)]] <- sprintf("mu_s[%d]", ci)
    if (per_geo_curves) {
      for (g in 1:G) {
        m[[sprintf("k:%s[%d]", ch, g)]] <- sprintf("k_raw[%d,%d]", ci, g)
        m[[sprintf("s:%s[%d]", ch, g)]] <- sprintf("s[%d,%d]", ci, g)
      }
    }
    m[[paste0("tau:", ch)]] <- sprintf("tau_roi[%d]", ci)
    m[[paste0("tau:decay:", ch)]] <- sprintf("tau_decay[%d]", ci)
    if (per_geo_curves) {
      m[[paste0("tau:k:", ch)]] <- sprintf("tau_k[%d]", ci)
      m[[paste0("tau:s:", ch)]] <- sprintf("tau_s[%d]", ci)
    }
  }
  m[["price"]] <- "gamma_raw[1]"
  m[["sigma"]] <- "sigma_raw"
  m
}

# roi_map is outputs.roi (each channel's all-geo figure, then each
# geo's); mu_map outputs.hierarchy.pooled.<channel>.mu (the population
# ROI).
roi_map <- function() {
  m <- list()
  for (ci in 1:2) {
    m[[channels[ci]]] <- sprintf("roi_all[%d]", ci)
    for (g in 1:G) m[[sprintf("%s[%d]", channels[ci], g)]] <- sprintf("roi[%d,%d]", ci, g)
  }
  m
}
mu_map <- function() list(tv = "mu_roi[1]", search = "mu_roi[2]")

chains <- 4
warmup <- 1500
draws <- 5000
out_cases <- list()
for (cs in cases) {
  sim <- simulate(cs$seed, cs$truth)
  d <- sim$rows
  ymax <- max(abs(d$sales))
  xscale <- c(max(d$tv), max(d$search))
  zmean <- mean(d$price)
  zscale <- max(abs(d$price - zmean))
  idx <- function(g) which(d$geo == g)
  spend <- array(0, dim = c(G, 2, N))
  zs <- array(0, dim = c(G, 1, N))
  ys <- matrix(0, G, N)
  spend_total <- matrix(0, G, 2)
  for (g in 1:G) {
    spend[g, 1, ] <- d$tv[idx(g)]
    spend[g, 2, ] <- d$search[idx(g)]
    zs[g, 1, ] <- (d$price[idx(g)] - zmean) / zscale
    ys[g, ] <- d$sales[idx(g)] / ymax
    spend_total[g, ] <- c(sum(d$tv[idx(g)]), sum(d$search[idx(g)]))
  }
  data <- list(G = G, N = N, C = 2L, J = 1L, L = L, per_geo_curves = as.integer(cs$per_geo_curves),
               ys = ys, spend = spend, xscale = xscale, share = spend_total / ymax, spend_total = spend_total,
               zs = zs, yscale = ymax, zmean = array(zmean), zscale = array(zscale))
  fit <- mod$sample(data = data, seed = cs$seed, chains = chains, parallel_chains = chains, iter_warmup = warmup,
                    iter_sampling = draws, adapt_delta = 0.95, refresh = 0, show_messages = FALSE)
  cm <- coef_map(cs$per_geo_curves)
  rm <- roi_map()
  mm <- mu_map()
  vars <- unique(c(unlist(cm), unlist(rm), unlist(mm)))
  sm <- summarise_draws(fit$draws(variables = vars), mean, sd, mcse_mean, rhat, ess_bulk)
  if (any(sm$rhat > 1.01)) stop("self-check failed: ", cs$name, " R-hat ", max(sm$rhat), " (", sm$variable[which.max(sm$rhat)], ")")
  if (any(sm$ess_bulk < 2000)) stop("self-check failed: ", cs$name, " bulk ESS ", min(sm$ess_bulk), " (", sm$variable[which.min(sm$ess_bulk)], ")")
  div <- sum(fit$diagnostic_summary(quiet = TRUE)$num_divergent)
  if (div > 0.005 * chains * draws) stop("self-check failed: ", cs$name, " ", div, " divergences")
  row <- function(v) {
    r <- sm[sm$variable == v, ]
    if (nrow(r) != 1) stop("no summary for ", v)
    list(mean = num(r$mean), sd = num(r$sd), mcse = num(r$mcse_mean))
  }
  figs <- function(m) {
    out <- list()
    for (nm in names(m)) out[[nm]] <- row(m[[nm]])
    out
  }
  params <- list(channels = as.list(channels), time = "week", geo = "geo", max_lag = L,
                 chains = 4L, warmup = 1000L, draws = 2500L)
  if (cs$per_geo_curves) params$per_geo_curves <- TRUE
  message(sprintf("%s: %d divergences, min ESS %.0f, max R-hat %.4f", cs$name, div, min(sm$ess_bulk), max(sm$rhat)))
  truth <- list(base = lapply(cs$truth$base, num))
  for (ch in channels) truth[[ch]] <- lapply(cs$truth[[ch]], function(v) lapply(v, num))
  truth$roi <- lapply(1:2, function(ci) lapply(sim$roi[, ci], num))
  names(truth$roi) <- channels
  out_cases[[length(out_cases) + 1]] <- list(
    name = cs$name, per_geo_curves = cs$per_geo_curves, rng_seed = cs$seed, params = params, truth = truth,
    rows = list(geo = d$geo, week = d$week, tv = lapply(d$tv, num), search = lapply(d$search, num),
                price = lapply(d$price, num), sales = lapply(d$sales, num)),
    stan = list(chains = chains, warmup = warmup, draws = draws, adapt_delta = 0.95, seed = cs$seed, divergences = div),
    coefficients = figs(cm), roi = figs(rm), mu = figs(mm))
}

doc <- list(
  generator = "scripts/reference/gen_mmm_bayes_geo_stan.R (make reference-stan-geo)",
  stan_program = "scripts/reference/mmm_bayes_geo.stan",
  r_version = R.version.string,
  packages = list(cmdstanr = as.character(packageVersion("cmdstanr")), cmdstan = cmdstan_version(),
                  posterior = as.character(packageVersion("posterior")), jsonlite = as.character(packageVersion("jsonlite"))),
  note = paste(
    "Posterior means (and SD, MCSE) of REG_MMM_BAYES's geo-panel coefficient table in original units, each channel's",
    "population ROI mu and its all-geo and per-geo ROI, sampled by CmdStan from an independent Stan program of the same",
    "non-centred hierarchical model and default priors on Pulse's scaled panel."),
  cases = out_cases)
path <- file.path(out_dir, "mmm_bayes_geo_stan.json")
cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
message("wrote ", path, " (", length(out_cases), " cases)")
