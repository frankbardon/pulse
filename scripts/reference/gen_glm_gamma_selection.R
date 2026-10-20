# gen_glm_gamma_selection.R — R oracle for the AIC / BIC a gamma REG_GLM
# `selection` scores its candidates by (reference-oracles E2-S5).
#
# Writes <out_dir>/glm_gamma_selection.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_glm_gamma_selection.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base stats +
# jsonlite only.
#
# R's AIC on a Gamma glm is the gamma log-likelihood at the dispersion
# dev / n (Gamma()$aic: n = sum(prior weights), disp = deviance / n,
# -2 sum(w log dgamma(y, 1 / disp, scale = mu disp)) + 2), counting the
# dispersion as a parameter: AIC(g) = that + 2 rank, BIC(g) = -2 logLik +
# log(nobs) (rank + 1). The pre-U36 Pulse score was deviance + 2 rank
# (deviance + log(n) rank), the fixed-dispersion form that is right for
# binomial / poisson only; it is kept per model as `phi1_aic` /
# `phi1_bic` for the before / after record.
#
# Weighted rows. Frequency = the rep() expansion: AIC / BIC of the glm on
# the expanded rows (self-check: AIC(glm(weights = f)) equals it, since
# Gamma()$aic multiplies by the prior weight and divides by sum(w); BIC
# does NOT — logLik.glm's nobs is the ROW count — so the expansion's
# log(sum f) is the frequency figure). Probability = the one weighting
# rule (`.claude/reference/weighting.md`): the frequency formula on
# w* = w n_eff / sum(w), so AIC = AIC(glm(weights = w*)) and BIC reads
# log(n_eff), Kish's effective n, in place of the row count.
#
# Selection. stats::step() on a Gamma glm ranks each candidate move by
# add1.glm / drop1.glm's deviance / phi_Pearson(current model) + k df
# (shifted to the current extractAIC), then refits the chosen move and
# stops unless its exact AIC drops. Pulse scores every candidate by its
# exact AIC(fit) / BIC(fit). The fixture below is chosen so the two agree
# — self-checked: an exact-AIC greedy search in R walks step()'s path —
# and so the fixed-dispersion score walks a DIFFERENT path (asserted).
# step(k = log(n))'s criterion is BIC(fit) minus the constant
# log(n) - 2 (it never adds log(n) for the dispersion), so its ranking is
# BIC's.
#
# Every fit is warm-started at its own converged coefficients
# (glm.control(epsilon = 1e-14)), so mu — and every likelihood figure —
# sits at the MLE, as in gen_glm_gamma.R.
#
# Seeded (set.seed(2611)) draws on its own after every other golden, so
# no earlier golden moves. Rows are written out as {x1, x2, x3, y, f, p}.

suppressPackageStartupMessages(library(jsonlite))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
    cat(txt, file = path)
    message("wrote ", path)
  }
}

gs_num <- function(x) {
  if (length(x) != 1) stop("gs_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
gs_near <- function(a, b, tol, what) {
  a <- as.numeric(a); b <- as.numeric(b)
  if (!all(abs(a - b) <= tol * pmax(abs(a), abs(b)))) {
    stop("self-check failed: ", what, " max rel diff ", max(abs(a - b) / pmax(abs(a), abs(b))))
  }
}

gs_ctl <- glm.control(epsilon = 1e-14, maxit = 100)
# gs_fit: Gamma(inverse) glm on `terms`, warm-started at its own MLE.
gs_fit <- function(terms, d, weights = NULL) {
  f <- reformulate(if (length(terms) == 0) "1" else terms, response = "y")
  d$.w <- if (is.null(weights)) rep(1, nrow(d)) else weights
  g <- glm(f, family = Gamma(), data = d, weights = .w, control = gs_ctl)
  if (!g$converged) stop("glm did not converge: ", deparse(f))
  g <- glm(f, family = Gamma(), data = d, weights = .w, start = coef(g), control = gs_ctl)
  if (!g$converged) stop("warm-started refit did not converge: ", deparse(f))
  g
}

set.seed(2611)
gs_n <- 40
gs_x1 <- round(runif(gs_n, 0, 2), 3)
gs_x2 <- round(rnorm(gs_n), 3)
gs_x3 <- round(rnorm(gs_n), 3)
gs_mu <- 1 / (0.5 + 0.25 * gs_x1 + 0.02 * gs_x2)
# Shape 40 (dispersion near 0.025): a deviance drop of 0.07 is worth
# ~0.07 / 0.02 = 3.5 AIC units, not 0.07 — the fixed-dispersion score
# cannot see x2.
gs_y <- round(rgamma(gs_n, shape = 40, rate = 40 / gs_mu), 4)
gs_f <- sample(1:4, gs_n, replace = TRUE)
gs_p <- round(runif(gs_n, 0.2, 3), 3)
gs_d <- data.frame(x1 = gs_x1, x2 = gs_x2, x3 = gs_x3, y = gs_y)
gs_dx <- gs_d[rep(seq_len(gs_n), gs_f), ]
gs_neff <- sum(gs_p)^2 / sum(gs_p^2)
gs_wstar <- gs_p * gs_neff / sum(gs_p)

gs_subsets <- list(character(0), "x1", "x2", "x3", c("x1", "x2"), c("x1", "x3"),
                   c("x2", "x3"), c("x1", "x2", "x3"))
gs_models <- lapply(gs_subsets, function(terms) {
  g <- gs_fit(terms, gs_d)
  k <- g$rank
  ll <- as.numeric(logLik(g))
  gs_near(AIC(g), -2 * ll + 2 * (k + 1), 1e-12, "AIC = -2 logLik + 2 (rank + 1)")
  gs_near(BIC(g), -2 * ll + log(gs_n) * (k + 1), 1e-12, "BIC = -2 logLik + log(n) (rank + 1)")
  # Frequency: the expansion; glm(weights = f) shares its AIC, not its BIC.
  gx <- gs_fit(terms, gs_dx)
  gf <- gs_fit(terms, gs_d, gs_f)
  gs_near(AIC(gf), AIC(gx), 1e-10, "AIC(glm(weights = f)) vs the expansion")
  # Probability: the frequency formula on w*, BIC on n_eff.
  gp <- gs_fit(terms, gs_d, gs_wstar)
  llp <- as.numeric(logLik(gp))
  list(
    predictors = I(terms), rank = gs_num(k),
    deviance = gs_num(deviance(g)), loglik = gs_num(ll),
    aic = gs_num(AIC(g)), bic = gs_num(BIC(g)),
    phi1_aic = gs_num(deviance(g) + 2 * k), phi1_bic = gs_num(deviance(g) + log(gs_n) * k),
    frequency_deviance = gs_num(deviance(gx)), frequency_loglik = gs_num(as.numeric(logLik(gx))),
    frequency_aic = gs_num(AIC(gx)), frequency_bic = gs_num(BIC(gx)),
    r_weights_frequency_bic = gs_num(BIC(gf)),
    probability_loglik = gs_num(llp), probability_aic = gs_num(AIC(gp)),
    probability_bic = gs_num(-2 * llp + log(gs_neff) * (k + 1)))
})

# Greedy search on a per-model score: forward from the intercept, backward
# from the full model, stepwise (both) from the intercept — Pulse's three
# drivers. Returns the final terms in the order they were added.
gs_score <- function(terms, kind) {
  g <- gs_fit(terms, gs_d)
  switch(kind,
    aic = AIC(g), bic = BIC(g),
    phi1_aic = deviance(g) + 2 * g$rank, phi1_bic = deviance(g) + log(gs_n) * g$rank)
}
gs_greedy <- function(direction, kind) {
  all <- c("x1", "x2", "x3")
  active <- if (direction == "backward") all else character(0)
  cur <- gs_score(active, kind)
  repeat {
    moves <- list()
    if (direction != "backward") for (v in setdiff(all, active)) moves[[length(moves) + 1]] <- c(active, v)
    if (direction != "forward") for (v in active) moves[[length(moves) + 1]] <- setdiff(active, v)
    if (length(moves) == 0) break
    sc <- vapply(moves, gs_score, numeric(1), kind = kind)
    if (min(sc) >= cur) break
    active <- moves[[which.min(sc)]]
    cur <- min(sc)
  }
  active
}
gs_step <- function(direction, kind) {
  k <- if (kind == "aic") 2 else log(gs_n)
  start <- if (direction == "backward") c("x1", "x2", "x3") else character(0)
  # A plain glm call on the global frame: step() re-evaluates it through
  # update(), which a warm `start` of the wrong length would break; the
  # path reads only the ranking.
  f0 <- reformulate(if (length(start) == 0) "1" else start, response = "y")
  g0 <- eval(bquote(glm(.(f0), family = Gamma(), data = gs_d, control = gs_ctl)))
  s <- step(g0, scope = list(lower = ~1, upper = ~ x1 + x2 + x3),
            direction = direction, k = k, trace = 0)
  list(terms = attr(terms(s), "term.labels"), path = as.character(s$anova$Step))
}

gs_runs <- list()
for (direction in c("forward", "backward", "both")) {
  for (crit in c("aic", "bic")) {
    st <- gs_step(direction, crit)
    exact <- gs_greedy(direction, crit)
    if (!setequal(exact, st$terms)) stop("exact-", crit, " greedy ", direction, " left step()'s path")
    if (direction != "backward" && !identical(exact, st$terms)) stop("addition order differs from step() ", direction)
    old <- gs_greedy(direction, paste0("phi1_", crit))
    gs_runs[[length(gs_runs) + 1]] <- list(
      selection = if (direction == "both") "stepwise" else direction, criterion = crit,
      r_step_terms = I(st$terms), r_step_path = I(st$path), phi1_terms = I(old))
  }
}
# The fixture must discriminate: the fixed-dispersion AIC walks elsewhere.
for (r in gs_runs) if (r$criterion == "aic" && setequal(r$phi1_terms, r$r_step_terms)) {
  stop("fixture does not discriminate for ", r$selection, " aic")
}

gs_rows <- lapply(seq_len(gs_n), function(i) list(
  x1 = gs_num(gs_x1[i]), x2 = gs_num(gs_x2[i]), x3 = gs_num(gs_x3[i]), y = gs_num(gs_y[i]),
  f = gs_num(gs_f[i]), p = gs_num(gs_p[i])))

gs_doc <- list(
  generator = "scripts/reference/gen_glm_gamma_selection.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_GLM gamma (inverse link) selection criterion: AIC(fit) / BIC(fit); step() path",
  r_function = "AIC(g); BIC(g); step(g, scope = list(lower = ~1, upper = ~ x1 + x2 + x3), direction, k = 2 | log(n))",
  note = paste(
    "aic / bic: AIC(g) / BIC(g) = -2 logLik + (2 | log(n)) (rank + 1), logLik from Gamma()$aic (dispersion deviance / n).",
    "phi1_*: deviance + (2 | log(n)) rank, the pre-U36 Pulse score. frequency_*: the rep() expansion (r_weights_frequency_bic: BIC(glm(weights = f)), whose nobs is the row count).",
    "probability_*: glm(weights = w*), w* = p n_eff / sum(p); BIC on log(n_eff).",
    "runs: step()'s final terms (addition order for forward / stepwise) and path; phi1_terms: the fixed-dispersion greedy's."),
  n_rows = gs_num(gs_n), n_eff = gs_num(gs_neff), rows = gs_rows,
  models = gs_models, runs = gs_runs)
write_golden("glm_gamma_selection", gs_doc)
