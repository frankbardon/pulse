# gen_glm_nb_refit.R — R oracle for the REG_GLM negative binomial under
# the selection and resample modifiers (U69 E3-S2, #638): theta
# re-estimated inside every selection candidate and every jackknife /
# bootstrap replicate (or held at a fixed theta).
#
# Writes <out_dir>/glm_nb_refit.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_glm_nb_refit.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite and MASS 7.3-66 (glm.nb, theta.ml,
# negative.binomial, stepAIC).
#
# Every fit is the joint MLE glm.nb approximates (gen_glm_nb.R): glm.nb
# first, then the same alternation pushed to its fixed point with MASS's
# own pieces (glm under negative.binomial(theta) refitted from its own
# optimum, theta.ml with eps 1e-12 * theta) until beta and theta move by
# under 1e-13 relative. A fixed theta is glm(negative.binomial(theta))
# pushed to its fixed point.
#
# Selection. MASS::stepAIC on a glm.nb fit (theta estimated) refits each
# candidate move with glm.nb (update(): theta re-estimated per
# candidate) and scores it by extractAIC.negbin, -2 logLik + k rank;
# Pulse scores -2 logLik + k (rank + 1), counting theta (glm.nb's aic) —
# a constant shift, the same ranking. Each run records stepAIC's final
# terms (addition order for forward / both) and path, self-checks that an
# exact greedy search over the fixed-point fits under Pulse's score walks
# the same terms, and pins the final model's fixed-point coefficients,
# theta and SE(theta). A FIXED theta has no exact R stepwise: step() and
# stepAIC on glm(negative.binomial(theta)) rank candidate moves by
# add1.glm / addterm.glm's one-step approximation (on this fixture step
# even accepts a move that raises the exact AIC), so its oracle is the
# exact greedy itself (-2 logLik + k rank over fixed-point glm fits,
# Pulse's score), asserted to differ from the estimated-theta run on the
# same direction and criterion (theta matters).
#
# Resample. A manual replicate loop: each replicate refits the model on
# its rows (theta re-estimated, or fixed) and records
# c(coefficients, theta-hat[, ame / mem of x1, x2]);
#   jackknife:  SE = sqrt((g - 1)/g * sum_k (r_(k) - rbar)^2) —
#               leave-one-out, delete-a-cluster (g) and the
#               frequency-weighted leave-one-out (glm.nb(weights = f))
#   bootstrap:  SE = sd(r_(b)) over B index sets the script draws and
#               records (samples, 0-based); the Go test drives Pulse's
#               replicate hook over exactly those sets
# theta_std_err is theta-hat's replicate SE (theta estimated only). The
# effects on the log link: ame = b_j mean_w(mu_i), mem = b_j mu(xbar'b),
# xbar the replicate rows' weighted means.
#
# A replicate whose fit warns stops the script, so no fixture pins a
# replicate Pulse would refuse. Seeded (set.seed(20261125)); standalone
# and sourced last, so no other golden moves. Rows are
# [x1, x2, x3, y, g, f].

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(MASS))

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    cat(toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE), file = path)
    message("wrote ", path)
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_glm_nb_refit.R pins R 4.6.1, got ", R.version.string)
if (as.character(packageVersion("MASS")) != "7.3.66") stop("gen_glm_nb_refit.R pins MASS 7.3-66, got ", packageVersion("MASS"))

nr_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
nr_num <- function(x) {
  if (length(x) != 1 || !is.finite(x)) stop("nr_num() takes a finite scalar")
  structure(sprintf("%.17g", x), class = "json")
}
nr_int <- function(v) structure(paste0("[", paste(as.integer(v), collapse = ","), "]"), class = "json")
nr_ctl <- glm.control(epsilon = 1e-14, maxit = 200)
nr_info <- function(th, mu, y, w) sum(w * (-trigamma(th + y) + trigamma(th) - 1 / th + 2 / (mu + th) - (y + th) / (mu + th)^2))
nr_loglik <- function(th, mu, y, w) sum(w * (lgamma(th + y) - lgamma(th) - lgamma(y + 1) + th * log(th) + y * log(mu + (y == 0)) - (th + y) * log(th + mu)))

# nr_quiet evaluates expr, stopping on any warning.
nr_quiet <- function(expr) withCallingHandlers(expr, warning = function(w) stop("fit warned: ", conditionMessage(w)))

# nr_glm is glm(negative.binomial(th)) refitted from start to its fixed point.
nr_glm <- function(f, th, d, start) {
  fam <- negative.binomial(th)
  m <- nr_quiet(glm(f, family = fam, data = d, weights = .w, control = nr_ctl, start = start))
  for (k in 1:500) {
    b <- coef(m)
    m <- nr_quiet(glm(f, family = fam, data = d, weights = .w, control = nr_ctl, start = b))
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-13) break
  }
  if (k == 500) stop("glm(negative.binomial) did not reach its fixed point")
  m
}

# nr_fit is the negative binomial on `terms` over d (prior weights w):
# theta estimated (fixed NULL) at the joint MLE, or fixed. Returns
# list(m, theta, se, ll, rank).
nr_fit <- function(terms, d, w, fixed = NULL) {
  f <- reformulate(if (length(terms) == 0) "1" else terms, response = "y")
  d$.w <- w
  if (!is.null(fixed)) {
    m0 <- nr_quiet(glm(f, family = negative.binomial(fixed), data = d, weights = .w, control = nr_ctl))
    m <- nr_glm(f, fixed, d, coef(m0))
    th <- fixed
    se <- 0
  } else {
    m0 <- nr_quiet(glm.nb(f, data = d, weights = .w, control = glm.control(maxit = 100)))
    th <- m0$theta
    b <- coef(m0)
    for (k in 1:500) {
      m <- nr_glm(f, th, d, b)
      th1 <- as.vector(theta.ml(m$y, fitted(m), sum(w), w, limit = 200, eps = 1e-12 * th))
      db <- max(abs(coef(m) - b) / pmax(abs(b), 1e-300))
      dt <- abs(th1 - th) / th
      th <- th1
      b <- coef(m)
      if (db <= 1e-13 && dt <= 1e-13) break
    }
    if (k == 500) stop("the glm.nb alternation did not reach its fixed point")
    m <- nr_glm(f, th, d, b)
    se <- 1 / sqrt(nr_info(th, fitted(m), m$y, w))
  }
  list(m = m, theta = th, se = se, ll = nr_loglik(th, fitted(m), m$y, w), rank = m$rank)
}

set.seed(20261125)
nr_n <- 80
nr_d <- data.frame(
  x1 = round(runif(nr_n, 0, 2), 3), x2 = round(rnorm(nr_n), 3), x3 = round(rnorm(nr_n), 3),
  g = rep(1:10, length.out = nr_n), f = sample(1:3, nr_n, replace = TRUE))
nr_d$y <- rnbinom(nr_n, size = 2.5, mu = exp(0.3 + 0.8 * nr_d$x1 + 0.2 * nr_d$x2))
nr_ones <- rep(1, nr_n)
nr_all <- c("x1", "x2", "x3")

# ---- selection ----
nr_score <- function(terms, k, fixed) {
  r <- nr_fit(terms, nr_d, nr_ones, fixed)
  -2 * r$ll + k * (r$rank + if (is.null(fixed)) 1 else 0)
}
nr_greedy <- function(direction, k, fixed) {
  active <- if (direction == "backward") nr_all else character(0)
  cur <- nr_score(active, k, fixed)
  repeat {
    moves <- list()
    if (direction != "backward") for (v in setdiff(nr_all, active)) moves[[length(moves) + 1]] <- c(active, v)
    if (direction != "forward") for (v in active) moves[[length(moves) + 1]] <- setdiff(active, v)
    if (length(moves) == 0) break
    sc <- vapply(moves, nr_score, numeric(1), k = k, fixed = fixed)
    if (min(sc) >= cur) break
    active <- moves[[which.min(sc)]]
    cur <- min(sc)
  }
  active
}
nr_step <- function(direction, k) {
  start <- if (direction == "backward") nr_all else character(0)
  f0 <- reformulate(if (length(start) == 0) "1" else start, response = "y")
  g0 <- eval(bquote(glm.nb(.(f0), data = nr_d)))
  s <- stepAIC(g0, scope = list(lower = ~1, upper = ~ x1 + x2 + x3), direction = direction, k = k, trace = 0)
  list(terms = attr(terms(s), "term.labels"), path = as.character(s$anova$Step))
}

nr_runs <- list()
nr_run <- function(direction, crit, fixed = NULL) {
  k <- if (crit == "aic") 2 else log(nr_n)
  exact <- nr_greedy(direction, k, fixed)
  path <- character(0)
  if (is.null(fixed)) {
    st <- nr_step(direction, k)
    if (!setequal(exact, st$terms)) stop("exact greedy ", direction, " ", crit, " left stepAIC's terms")
    if (direction != "backward" && !identical(exact, st$terms)) stop("addition order differs from stepAIC ", direction)
    path <- st$path
  } else if (setequal(exact, nr_greedy(direction, k, NULL))) {
    stop("fixed-theta ", direction, " ", crit, " walks the estimated-theta terms; the fixture does not discriminate")
  }
  r <- nr_fit(exact, nr_d, nr_ones, fixed)
  nr_runs[[length(nr_runs) + 1]] <<- list(
    selection = if (direction == "both") "stepwise" else direction, criterion = crit,
    theta_fixed = nr_num(if (is.null(fixed)) 0 else fixed),
    oracle = if (is.null(fixed)) "stepAIC" else "exact_greedy",
    terms = I(exact), r_step_path = I(path),
    coefficients = nr_g17(coef(r$m)), theta = nr_num(r$theta), theta_std_err = nr_num(r$se))
}
for (direction in c("forward", "backward", "both")) for (crit in c("aic", "bic")) nr_run(direction, crit)
nr_run("backward", "aic", fixed = 2)

# ---- resample ----
nr_X <- function(d) model.matrix(~ x1 + x2, d)
nr_replicate <- function(idx, w, fixed, margins) {
  dd <- nr_d[idx, ]
  ww <- w[idx]
  r <- nr_fit(c("x1", "x2"), dd, ww, fixed)
  b <- coef(r$m)
  out <- c(b, r$theta)
  if (!is.null(margins)) {
    X <- nr_X(dd)
    eff <- if (margins == "ame") {
      b[2:3] * sum(ww * exp(drop(X %*% b))) / sum(ww)
    } else {
      xbar <- colSums(ww * X) / sum(ww)
      b[2:3] * exp(sum(xbar * b))
    }
    out <- c(out, eff)
  }
  out
}
nr_cases <- list()
nr_case <- function(name, resample, fixed = NULL, by = FALSE, weighted = FALSE, margins = NULL, B = 40) {
  w <- if (weighted) nr_d$f else nr_ones
  full <- nr_replicate(seq_len(nr_n), w, fixed, margins)
  out <- list(name = name, resample = resample, theta_fixed = nr_num(if (is.null(fixed)) 0 else fixed),
    jackknife_by = by, weighted = weighted, margins = if (is.null(margins)) "" else margins)
  if (resample == "jackknife") {
    groups <- if (by) split(seq_len(nr_n), nr_d$g) else as.list(seq_len(nr_n))
    reps <- t(sapply(groups, function(gi) nr_replicate(setdiff(seq_len(nr_n), gi), w, fixed, margins)))
    g <- nrow(reps)
    se <- sqrt((g - 1) / g * colSums(sweep(reps, 2, colMeans(reps))^2))
  } else {
    # Draw until B index sets refit cleanly: a set whose fit warns (theta
    # running off to infinity on an unlucky draw) is discarded here —
    # Pulse's bootstrap skips such a replicate too — so every recorded
    # set is one both sides fit.
    samples <- list()
    reps <- NULL
    while (length(samples) < B) {
      s <- sample.int(nr_n, nr_n, replace = TRUE)
      r <- tryCatch(nr_replicate(s, w, fixed, margins), error = function(e) NULL)
      if (is.null(r)) next
      samples[[length(samples) + 1]] <- s
      reps <- rbind(reps, r)
    }
    se <- apply(reps, 2, sd)
    out$samples <- lapply(samples, function(s) nr_int(s - 1))
  }
  out$coefficients <- nr_g17(full[1:3])
  out$coef_std_errors <- nr_g17(se[1:3])
  out$theta <- nr_num(full[4])
  if (is.null(fixed)) out$theta_std_err <- nr_num(se[4])
  if (!is.null(margins)) {
    out$margin_estimates <- nr_g17(full[5:6])
    out$margin_std_errors <- nr_g17(se[5:6])
  }
  nr_cases[[length(nr_cases) + 1]] <<- out
}
nr_case("jackknife", "jackknife")
nr_case("jackknife_theta_fixed", "jackknife", fixed = 2)
nr_case("jackknife_by", "jackknife", by = TRUE)
nr_case("jackknife_freq", "jackknife", weighted = TRUE)
nr_case("bootstrap", "bootstrap")
nr_case("bootstrap_theta_fixed", "bootstrap", fixed = 2)
nr_case("jackknife_ame", "jackknife", margins = "ame")
nr_case("bootstrap_mem", "bootstrap", margins = "mem")

nr_rows <- lapply(seq_len(nr_n), function(i) nr_g17(c(nr_d$x1[i], nr_d$x2[i], nr_d$x3[i], nr_d$y[i], nr_d$g[i], nr_d$f[i])))

write_golden("glm_nb_refit", list(
  generator = "scripts/reference/gen_glm_nb_refit.R (via gen_reference.R)",
  description = paste(
    "REG_GLM negative_binomial under selection and resample (U69 E3-S2): theta re-estimated in every",
    "candidate / replicate at the joint MLE (or fixed). runs: stepAIC on glm.nb (oracle stepAIC; the",
    "exact greedy on Pulse's score agrees) or, theta fixed, the exact greedy (oracle exact_greedy) —",
    "final terms (addition order), stepAIC's path, the final model's coefficients",
    "(intercept then terms), theta, SE(theta) (0 fixed). cases: manual replicate loops of",
    "c(coefficients, theta[, ame / mem]); jackknife SE sqrt((g-1)/g sum (r_k - rbar)^2), bootstrap SE",
    "sd over the recorded index sets (samples, 0-based). Rows are [x1, x2, x3, y, g, f]."),
  packages = list(R = R.version.string, MASS = as.character(packageVersion("MASS")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  seed = 20261125, rows = nr_rows, runs = nr_runs, cases = nr_cases))
