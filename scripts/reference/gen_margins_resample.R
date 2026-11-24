# gen_margins_resample.R — R oracle for marginal effects under the
# Resample modifier (RegressionSpec.Margins beside resample, U69 #639).
#
# Every case is a MANUAL replicate loop in base R: each replicate refits
# the model (lm / glm) on its rows and recomputes the effect itself —
# the average marginal effect (ame: the replicate rows' prior-weighted
# mean of b_j mu'(eta_i)), the effect at the mean (mem: b_j mu'(xbar'b)
# at the replicate rows' weighted means) or, under binary = "contrast",
# the discrete contrast mu(x_j = 1) - mu(x_j = 0) of a 0/1 column — and
#
#   jackknife:  SE = sqrt((g - 1)/g * sum_k (e_(k) - ebar)^2), p the
#               two-sided Wald z of the FULL-data effect over SE
#   bootstrap:  SE = sd(e_(b)) (the B - 1 divisor), p the percentile
#               2 * min(mean(e_(b) < 0), 1 - mean(e_(b) < 0)), floored
#               at 1/B
#
# the interval the full-data effect -/+ qnorm(0.975) SE (Wald normal at
# 0.95) — the resampled coefficient table's rules. On an identity link
# (lm) the effect is the coefficient, so the case also pins the
# coefficient's replicate SE (coef_std_errors) and the two agree.
#
# The jackknife cases are leave-one-out (every row its own group), one
# delete-a-cluster case (jackknife_by = g, 12 clusters) and a
# frequency-weighted leave-one-out case (glm(weights = f) on the
# remaining rows, JK1). The bootstrap's draws are R's own: each case
# records its B index sets (samples, 0-based rows of `rows`), drawn with
# sample.int under the script's seed, and the Go test runs Pulse's
# replicate hook and replicate summary over exactly those sets — Pulse's
# own PCG draws are pinned seed-stable by same-process tests instead.
#
# Writes <out_dir>/margins_resample.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_margins_resample.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned: R 4.6.1, base stats + jsonlite. glm fits use
# glm.control(epsilon = 1e-14, maxit = 200); a replicate that warns
# (separation, no convergence) stops the script, so no fixture pins a
# replicate Pulse would refuse. Seeded (set.seed(20261124)); standalone
# and sourced last, so no other golden moves. Rows are [x1, x2, y, g, f]
# (g the cluster id, f the frequency weight; 1 when unweighted).

suppressPackageStartupMessages(library(jsonlite))

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_margins_resample.R pins R 4.6.1, got ", R.version.string)

mr_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
mr_int <- function(v) structure(paste0("[", paste(as.integer(v), collapse = ","), "]"), class = "json")

mr_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# mr_fit fits model on d (prior weights w), stopping on any warning.
mr_fit <- function(model, family, d, w) {
  d$.w <- w
  withCallingHandlers(
    if (model == "lm") lm(y ~ x1 + x2, data = d, weights = .w)
    else glm(y ~ x1 + x2, family = family, data = d, weights = .w, control = mr_ctl),
    warning = function(w) stop("replicate fit warned: ", conditionMessage(w)))
}

# mr_effects is the effect of x1 and x2 of fit m over its design X with
# prior weights w: type ame | mem, binary slope | contrast.
mr_effects <- function(m, X, w, type, binary) {
  b <- coef(m)
  fam <- if (inherits(m, "glm")) m$family else gaussian()
  sw <- sum(w)
  xbar <- colSums(w * X) / sw
  sapply(c("x1", "x2"), function(t) {
    j <- match(t, colnames(X))
    if (binary == "contrast" && all(X[, j] %in% c(0, 1))) {
      pts <- if (type == "mem") matrix(xbar, nrow = 1) else X
      ww <- if (type == "mem") 1 else w
      p1 <- pts; p1[, j] <- 1
      p0 <- pts; p0[, j] <- 0
      return(sum(ww * (fam$linkinv(drop(p1 %*% b)) - fam$linkinv(drop(p0 %*% b)))) / sum(ww))
    }
    if (type == "mem") return(b[j] * fam$mu.eta(sum(xbar * b)))
    b[j] * sum(w * fam$mu.eta(drop(X %*% b))) / sw
  })
}

# mr_replicate refits on rows idx of d and returns c(coefficients, effects).
mr_replicate <- function(model, family, d, w, idx, type, binary) {
  dd <- d[idx, ]
  ww <- w[idx]
  m <- mr_fit(model, family, dd, ww)
  X <- model.matrix(~ x1 + x2, dd)
  c(coef(m), mr_effects(m, X, ww, type, binary))
}

mr_cases <- list()

# mr_case runs one case: resample jackknife (leave-one-out, or by the
# cluster g) or bootstrap (B index sets).
mr_case <- function(name, model, family, link, d, resample, type = "ame", binary = "slope",
                    by = FALSE, weighted = FALSE, B = 200) {
  n <- nrow(d)
  w <- if (weighted) d$f else rep(1, n)
  full <- mr_replicate(model, family, d, w, seq_len(n), type, binary)
  est <- full[4:5]
  q <- qnorm(0.975)
  out <- list(name = name, model = model, family = if (model == "lm") "" else tolower(family$family), link = link,
    type = type, binary = binary, resample = resample, jackknife_by = by, weighted = weighted)
  if (resample == "jackknife") {
    groups <- if (by) split(seq_len(n), d$g) else as.list(seq_len(n))
    reps <- t(sapply(groups, function(gi) mr_replicate(model, family, d, w, setdiff(seq_len(n), gi), type, binary)))
    g <- nrow(reps)
    se <- sqrt((g - 1) / g * colSums(sweep(reps, 2, colMeans(reps))^2))
    p <- 2 * pnorm(-abs(est / se[4:5]))
  } else {
    samples <- lapply(seq_len(B), function(b) sample.int(n, n, replace = TRUE))
    reps <- t(sapply(samples, function(s) mr_replicate(model, family, d, w, s, type, binary)))
    se <- apply(reps, 2, sd)
    p <- sapply(4:5, function(j) {
      if (min(abs(reps[, j])) < 1e-6) stop(name, ": a replicate effect sits at zero; the percentile p is not robust")
      ph <- mean(reps[, j] < 0)
      max(2 * min(ph, 1 - ph), 1 / B)
    })
    out$samples <- lapply(samples, function(s) mr_int(s - 1))
  }
  if (model == "lm") {
    # An identity link: the effect IS the coefficient.
    if (max(abs(est - full[2:3])) > 1e-12 || max(abs(se[4:5] - se[2:3]) / se[2:3]) > 1e-12) stop(name, ": lm effect is not its coefficient")
  }
  out$rows <- lapply(seq_len(n), function(i) mr_g17(c(d$x1[i], d$x2[i], d$y[i], d$g[i], w[i])))
  out$coefficients <- mr_g17(full[1:3])
  out$coef_std_errors <- mr_g17(se[1:3])
  out$estimates <- mr_g17(est)
  out$std_errors <- mr_g17(se[4:5])
  out$p_values <- mr_g17(p)
  out$ci_lower <- mr_g17(est - q * se[4:5])
  out$ci_upper <- mr_g17(est + q * se[4:5])
  mr_cases[[length(mr_cases) + 1]] <<- out
}

set.seed(20261124)
mr_design <- function(n) data.frame(x1 = round(runif(n, -1, 2), 3), x2 = round(runif(n, -1, 1), 3),
                                     g = rep(1:12, length.out = n), f = sample(1:3, n, replace = TRUE))

d <- mr_design(60)
d$y <- 1 + 0.8 * d$x1 - 0.5 * d$x2 + rnorm(60, sd = 0.7)
mr_case("ols_ame_jackknife", "lm", NULL, "identity", d, "jackknife")
mr_case("ols_mem_bootstrap", "lm", NULL, "identity", d, "bootstrap", type = "mem")

d <- mr_design(120)
d$y <- rbinom(120, 1, plogis(-0.3 + 1.1 * d$x1 - 0.9 * d$x2))
mr_case("logit_ame_jackknife", "glm", binomial("logit"), "logit", d, "jackknife")
mr_case("logit_mem_jackknife", "glm", binomial("logit"), "logit", d, "jackknife", type = "mem")
mr_case("logit_ame_jackknife_by", "glm", binomial("logit"), "logit", d, "jackknife", by = TRUE)
mr_case("logit_ame_jackknife_freq", "glm", binomial("logit"), "logit", d, "jackknife", weighted = TRUE)
mr_case("logit_ame_bootstrap", "glm", binomial("logit"), "logit", d, "bootstrap")
mr_case("logit_mem_bootstrap", "glm", binomial("logit"), "logit", d, "bootstrap", type = "mem")
mr_case("probit_ame_jackknife", "glm", binomial("probit"), "probit", d, "jackknife")

d <- mr_design(120)
d$x2 <- rbinom(120, 1, 0.5)
d$y <- rbinom(120, 1, plogis(-0.4 + 1.0 * d$x1 + 0.9 * d$x2))
mr_case("logit_ame_contrast_jackknife", "glm", binomial("logit"), "logit", d, "jackknife", binary = "contrast")
mr_case("logit_mem_contrast_bootstrap", "glm", binomial("logit"), "logit", d, "bootstrap", type = "mem", binary = "contrast")

d <- mr_design(100)
d$y <- rpois(100, exp(0.4 + 0.5 * d$x1 - 0.6 * d$x2))
mr_case("poisson_mem_jackknife", "glm", poisson("log"), "log", d, "jackknife", type = "mem")
mr_case("poisson_ame_bootstrap", "glm", poisson("log"), "log", d, "bootstrap")

write_golden("margins_resample", list(
  description = paste(
    "Marginal effects under resample (U69 #639): manual replicate loops in base R, each replicate",
    "refitting lm / glm on its rows and recomputing the effect (ame, mem or the 0/1 contrast) itself.",
    "Jackknife: SE = sqrt((g-1)/g sum (e_k - ebar)^2), p the Wald z of the full-data effect.",
    "Bootstrap: SE = sd over the recorded index sets (samples, 0-based), p the percentile p floored at 1/B.",
    "Interval: full-data effect -/+ qnorm(0.975) SE. Rows are [x1, x2, y, g, f]."),
  packages = list(R = R.version.string, jsonlite = as.character(packageVersion("jsonlite"))),
  seed = 20261124,
  cases = mr_cases
))
