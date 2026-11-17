# gen_nested.R — R oracle for the Compose nested-model comparison
# (ComposedRequest.Compare, U65 E3-S3, #629): two regression fits on the
# same rows, the restricted model's terms a subset of the full model's,
# compared by
#   f     OLS anova(m0, m1) (F on the full fit's sigma^2); GLM
#         anova(m0, m1, test = "F") (the full fit's Pearson dispersion,
#         its residual df) on a family that estimates the dispersion
#   lrt   OLS anova(m0, m1, test = "Chisq") (the RSS change over the full
#         fit's sigma^2); GLM anova(m0, m1, test = "LRT") (the deviance
#         change over the dispersion: 1 binomial / poisson / a fixed-
#         dispersion negative binomial, the Pearson estimate on gamma);
#         a negative binomial with theta estimated MASS's anova.negbin
#         (2 loglik change, each model at its own theta-hat)
#   wald  lmtest::waldtest(m0, m1, vcov = V, test = "F" | "Chisq") — the
#         full fit's tested coefficients b_S against V_SS, V the
#         classical vcov or sandwich::vcovHC(type = "HC3"); F (W / q on
#         the full fit's residual df) where the coefficient table is
#         Student t (lm, gamma), chi-square otherwise.
#
# Writes <out_dir>/nested.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_nested.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite 2.0.0 + MASS 7.3-66 (glm.nb, theta.ml,
# negative.binomial, anova.negbin; polr and anova.polr for the ordinal
# block) + sandwich 3.1-3 (vcovHC; sandwich on polr) + lmtest
# 0.9-40 (waldtest, lrtest; with zoo 1.9-1) + nnet 7.3-21 (multinom and
# anova.multinom for the multinomial block), installed from the Posit
# noble binary repo; versions recorded in the golden.
#
# Every figure is re-derived from its closed form and the script stops on
# a mismatch: the F / chi-square statistics from the two fits' RSS or
# deviance, the dispersion and the residual df; the Wald statistic as
# t(b_S) solve(V_SS) b_S. anova / waldtest / lrtest are the references;
# lrtest (2 loglik change) agrees with the deviance change exactly on
# binomial / poisson / a fixed-theta negative binomial (asserted), never
# on gamma (each model's own phi = D / n), which is why the gamma lrt is
# anova's.
#
# Frequency-weighted cases ("_freq", integer w) are fitted on the
# rep()-expanded rows (the Pulse convention: N* = sum(w), so the residual
# df is sum(w) - rank, as R's df on the expansion; lm / glm(weights = w)
# would read n - rank) and the compact weighted fit is asserted to give
# the same coefficients. GLM fits are refitted from their own optimum
# until the coefficients stop moving (glm.control(epsilon = 1e-14)); a
# negative binomial with theta estimated is glm.nb pushed to its joint
# fixed point (the gen_glm_nb.R alternation).
#
# Seeded (set.seed(20261113)); draws its own rows, so no other golden
# moves. Rows are [x1, x2, x3, y, w] (w = 1 unweighted).

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(MASS))
suppressPackageStartupMessages(library(sandwich))
suppressPackageStartupMessages(library(lmtest))
suppressPackageStartupMessages(library(nnet))

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_nested.R pins R 4.6.1, got ", R.version.string)
nested_pins <- c(lmtest = "0.9.40", zoo = "1.9.1", sandwich = "3.1.3", MASS = "7.3.66", jsonlite = "2.0.0", nnet = "7.3.21")
for (p in names(nested_pins)) {
  if (as.character(packageVersion(p)) != nested_pins[[p]]) stop("gen_nested.R pins ", p, " ", nested_pins[[p]], ", got ", packageVersion(p))
}

nested_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
nested_num <- function(v) {
  if (is.null(v)) return(NULL)
  v <- unname(as.numeric(v))
  if (length(v) != 1 || !is.finite(v)) stop("non-finite reference scalar")
  structure(sprintf("%.17g", v), class = "json")
}
nested_check <- function(what, got, want, tol = 1e-10) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  if (max(abs(got - want) / pmax(1e-300, abs(want))) > tol) stop(what, ": closed form disagrees with R (", got, " vs ", want, ")")
}
nested_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# nested_glm fits f on d by glm, refitted to its fixed point.
nested_glm <- function(f, d, family) {
  m <- suppressWarnings(glm(f, family = family, data = d, control = nested_ctl))
  for (k in 1:500) {
    b <- coef(m)
    m <- suppressWarnings(glm(f, family = family, data = d, control = nested_ctl, start = b))
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-15) break
  }
  m <- suppressWarnings(glm(f, family = family, data = d, control = nested_ctl, start = coef(m)))
  if (!m$converged) stop("glm did not converge")
  m
}

nb_loglik <- function(th, mu, y) sum(lgamma(th + y) - lgamma(th) - lgamma(y + 1) + th * log(th) + y * log(mu + (y == 0)) - (th + y) * log(th + mu))
nb_score <- function(th, mu, y) sum(digamma(th + y) - digamma(th) + log(th) + 1 - log(th + mu) - (y + th) / (mu + th))
nb_info <- function(th, mu, y) sum(-trigamma(th + y) + trigamma(th) - 1 / th + 2 / (mu + th) - (y + th) / (mu + th)^2)

# nested_nb is the joint MLE glm.nb approximates (gen_glm_nb.R's
# alternation): glm.nb, then IRLS at theta and theta.ml at mu until
# neither moves. Returns the final glm, negbin-classed with theta and
# twologlik, and glm.nb's own fit (the anova.negbin / lrtest cross-check).
nested_nb <- function(f, d) {
  m0 <- glm.nb(f, data = d, control = glm.control(maxit = 100))
  th <- m0$theta
  b <- coef(m0)
  for (k in 1:500) {
    m <- nested_glm(f, d, negative.binomial(th))
    th1 <- as.vector(theta.ml(m$y, fitted(m), nrow(d), limit = 200, eps = 1e-12 * th))
    db <- max(abs(coef(m) - b) / pmax(abs(b), 1e-300))
    dt <- abs(th1 - th) / th
    th <- th1
    b <- coef(m)
    if (db <= 1e-13 && dt <= 1e-13) break
  }
  if (k == 500) stop("the glm.nb alternation did not reach its fixed point")
  m <- nested_glm(f, d, negative.binomial(th))
  if (abs(nb_score(th, fitted(m), m$y)) > 1e-9 * abs(nb_info(th, fitted(m), m$y))) stop("theta is not at its score root")
  m$theta <- th
  m$twologlik <- 2 * nb_loglik(th, fitted(m), m$y)
  nested_check("glm.nb vs its fixed point (theta)", m0$theta, th, 1e-4)
  list(m = m, loose = m0)
}

nested_cases <- list()

# nested_formula is y on the predictors, through the origin unless
# intercept.
nested_formula <- function(preds, intercept = TRUE) {
  rhs <- paste(preds, collapse = " + ")
  as.formula(paste("y ~", if (intercept) rhs else paste("0 +", rhs)))
}

# nested_case records one comparison. family NULL is lm; theta 0 with a
# negative binomial family estimates theta; dispersion "estimated" on a
# fixed-theta negative binomial is the Pearson estimate (test f / lrt
# read it); covariance "" (classical) or "HC3" (the Wald V); kind ""
# (unweighted) or "frequency" (d$w integer: fitted on the expansion).
nested_case <- function(name, d, restricted, full, test, family = NULL, theta = 0, dispersion = "",
                        covariance = "", kind = "", restricted_intercept = TRUE) {
  if (kind == "") d$w <- 1
  ex <- if (kind == "frequency") d[rep(seq_len(nrow(d)), d$w), ] else d
  f0 <- nested_formula(restricted, restricted_intercept)
  f1 <- nested_formula(full)
  nb_est <- !is.null(family) && identical(family, "negbin")
  fixed_nb <- !is.null(family) && !nb_est && grepl("^Negative Binomial", family$family)
  if (is.null(family)) {
    m0 <- lm(f0, data = ex); m1 <- lm(f1, data = ex)
    if (kind == "frequency") nested_check(paste(name, "weighted vs expanded"), coef(lm(f1, data = d, weights = w)), coef(m1), 1e-9)
  } else if (nb_est) {
    r0 <- nested_nb(f0, ex); r1 <- nested_nb(f1, ex)
    m0 <- r0$m; m1 <- r1$m
  } else {
    m0 <- nested_glm(f0, ex, family); m1 <- nested_glm(f1, ex, family)
    if (kind == "frequency") {
      mw <- suppressWarnings(glm(f1, family = family, data = d, weights = w, control = nested_ctl, start = coef(m1)))
      nested_check(paste(name, "weighted vs expanded"), coef(mw), coef(m1), 1e-9)
    }
  }
  n <- nrow(ex)
  rdf0 <- df.residual(m0); rdf1 <- df.residual(m1)
  dev0 <- deviance(m0); dev1 <- deviance(m1)
  q <- rdf0 - rdf1
  terms <- setdiff(names(coef(m1)), names(coef(m0)))
  if (length(terms) != q) stop(name, ": tested terms do not match the df change")
  out <- list(name = name, test = test, n = n)
  disp <- NULL; stat <- NULL; df2 <- NULL; p <- NULL; dist <- NULL; tll <- NULL
  if (is.null(family)) {
    disp <- dev1 / rdf1
    if (test == "f") {
      a <- anova(m0, m1)
      stat <- a$F[2]; p <- a$`Pr(>F)`[2]; df2 <- rdf1; dist <- "f"
      nested_check(paste(name, "F closed form"), ((dev0 - dev1) / q) / disp, stat)
    } else if (test == "lrt") {
      a <- anova(m0, m1, test = "Chisq")
      stat <- (dev0 - dev1) / disp; p <- a$`Pr(>Chi)`[2]; dist <- "chisq"
      nested_check(paste(name, "chisq p"), pchisq(stat, q, lower.tail = FALSE), p)
    }
  } else if (nb_est) {
    if (test == "lrt") {
      stat <- m1$twologlik - m0$twologlik; p <- pchisq(stat, q, lower.tail = FALSE); dist <- "chisq"
      tll <- c(m0$twologlik, m1$twologlik)
      a <- anova(r0$loose, r1$loose)
      nested_check(paste(name, "anova.negbin LR (loose)"), a$`LR stat.`[2], stat, 1e-5)
      lr <- lrtest(r0$loose, r1$loose)
      nested_check(paste(name, "lrtest (loose)"), lr$Chisq[2], stat, 1e-5)
    }
  } else {
    fam <- family$family
    fixed_phi <- fam %in% c("binomial", "poisson") || (fixed_nb && dispersion != "estimated")
    disp <- if (fixed_phi) 1 else sum(residuals(m1, type = "pearson")^2) / rdf1
    if (!fixed_phi) nested_check(paste(name, "Pearson dispersion"), disp, summary(m1)$dispersion)
    if (test == "f") {
      a <- anova(m0, m1, test = "F", dispersion = if (fixed_nb) disp else NULL)
      stat <- a$F[2]; p <- a$`Pr(>F)`[2]; df2 <- rdf1; dist <- "f"
      nested_check(paste(name, "F closed form"), ((dev0 - dev1) / q) / disp, stat)
      nested_check(paste(name, "F p"), pf(stat, q, rdf1, lower.tail = FALSE), p)
    } else if (test == "lrt") {
      a <- anova(m0, m1, test = "LRT", dispersion = if (fixed_nb) disp else NULL)
      stat <- (dev0 - dev1) / disp; p <- a$`Pr(>Chi)`[2]; dist <- "chisq"
      nested_check(paste(name, "chisq p"), pchisq(stat, q, lower.tail = FALSE), p)
      if (fixed_phi) {
        lr <- lrtest(m0, m1)
        nested_check(paste(name, "lrtest = deviance change"), lr$Chisq[2], stat, 1e-9)
      }
    }
  }
  if (test == "wald") {
    V <- if (covariance == "HC3") vcovHC(m1, type = "HC3") else vcov(m1)
    # A negative binomial's classical vcov is at dispersion 1
    # (summary.negbin; a fixed theta's too unless dispersion "estimated").
    if (nb_est || (fixed_nb && dispersion != "estimated")) V <- vcov(m1, dispersion = 1)
    b <- coef(m1)[terms]
    W <- as.numeric(t(b) %*% solve(V[terms, terms, drop = FALSE], b))
    student <- is.null(family) || (!nb_est && family$family == "Gamma") || (fixed_nb && dispersion == "estimated")
    if (student) {
      wt <- waldtest(m0, m1, vcov = V, test = "F")
      stat <- wt$F[2]; p <- wt$`Pr(>F)`[2]; df2 <- rdf1; dist <- "f"
      nested_check(paste(name, "Wald F closed form"), W / q, stat)
    } else {
      wt <- waldtest(m0, m1, vcov = V, test = "Chisq")
      stat <- wt$Chisq[2]; p <- wt$`Pr(>Chisq)`[2]; dist <- "chisq"
      nested_check(paste(name, "Wald chisq closed form"), W, stat)
    }
    disp <- NULL
  }
  if (is.null(stat) || is.null(p)) stop(name, ": no statistic")
  famname <- if (is.null(family)) "gaussian" else if (nb_est || fixed_nb) "negative_binomial" else tolower(family$family)
  link <- if (is.null(family)) "identity" else if (nb_est) "log" else family$link
  rows <- lapply(seq_len(nrow(d)), function(i) nested_g17(c(d$x1[i], d$x2[i], d$x3[i], d$y[i], d$w[i])))
  # Pulse keys the intercept "(intercept)"; R's "(Intercept)".
  terms <- sub("^\\(Intercept\\)$", "(intercept)", terms)
  case <- c(out, list(
    family = famname, link = link, theta = theta, dispersion = dispersion, covariance = covariance, weight = kind,
    restricted = I(restricted), full = I(full), restricted_intercept = restricted_intercept,
    terms = I(terms), distribution = dist, rows = rows,
    statistic = nested_num(stat), df = nested_num(q), df_residual = nested_num(df2), p_value = nested_num(p),
    restricted_df_residual = nested_num(rdf0), full_df_residual = nested_num(rdf1),
    restricted_deviance = nested_num(dev0), full_deviance = nested_num(dev1),
    dispersion_used = nested_num(disp),
    restricted_two_log_lik = nested_num(tll[1]), full_two_log_lik = nested_num(tll[2])))
  # An absent figure (NULL) is no key, never {}.
  nested_cases[[length(nested_cases) + 1]] <<- Filter(Negate(is.null), case)
}

set.seed(20261113)
# nested_design draws n rows: x2 leans on x1, x3 on both.
nested_design <- function(n) {
  x1 <- round(rnorm(n), 4)
  x2 <- round(0.5 * x1 + rnorm(n, sd = 0.9), 4)
  x3 <- round(0.3 * x1 - 0.4 * x2 + rnorm(n, sd = 0.8), 4)
  data.frame(x1 = x1, x2 = x2, x3 = x3)
}
eta_of <- function(d, b) b[1] + b[2] * d$x1 + b[3] * d$x2 + b[4] * d$x3

d <- nested_design(60)
d$y <- round(eta_of(d, c(1, 0.8, -0.4, 0.25)) + rnorm(60), 4)
nested_case("ols_f", d, "x1", c("x1", "x2", "x3"), "f")
nested_case("ols_lrt", d, "x1", c("x1", "x2", "x3"), "lrt")
nested_case("ols_wald", d, "x1", c("x1", "x2", "x3"), "wald")
nested_case("ols_wald_hc3", d, "x1", c("x1", "x2", "x3"), "wald", covariance = "HC3")
nested_case("ols_origin_f", d, "x1", c("x1", "x2"), "f", restricted_intercept = FALSE)
dw <- d[1:40, ]; dw$w <- sample(1:4, 40, replace = TRUE)
nested_case("ols_f_freq", dw, c("x1", "x2"), c("x1", "x2", "x3"), "f", kind = "frequency")

d <- nested_design(150)
d$y <- rbinom(150, 1, plogis(eta_of(d, c(-0.2, 0.9, -0.5, 0.4))))
nested_case("logit_lrt", d, "x1", c("x1", "x2", "x3"), "lrt", family = binomial())
nested_case("logit_wald", d, "x1", c("x1", "x2", "x3"), "wald", family = binomial())
nested_case("logit_wald_hc3", d, "x1", c("x1", "x2", "x3"), "wald", family = binomial(), covariance = "HC3")
nested_case("probit_lrt", d, c("x1", "x2"), c("x1", "x2", "x3"), "lrt", family = binomial(link = "probit"))

d <- nested_design(100)
d$y <- rpois(100, exp(eta_of(d, c(0.4, 0.5, -0.3, 0.2))))
nested_case("poisson_lrt", d, "x1", c("x1", "x2", "x3"), "lrt", family = poisson())
nested_case("poisson_wald", d, "x1", c("x1", "x2", "x3"), "wald", family = poisson())
dw <- d[1:50, ]; dw$w <- sample(1:4, 50, replace = TRUE)
nested_case("poisson_lrt_freq", dw, "x1", c("x1", "x2", "x3"), "lrt", family = poisson(), kind = "frequency")

d <- nested_design(90)
d$y <- round(rgamma(90, shape = 4, rate = 4 * (1.5 + 0.2 * d$x1 - 0.1 * d$x2 + 0.1 * d$x3)), 5)
nested_case("gamma_f", d, "x1", c("x1", "x2", "x3"), "f", family = Gamma())
nested_case("gamma_lrt", d, "x1", c("x1", "x2", "x3"), "lrt", family = Gamma())
nested_case("gamma_wald", d, "x1", c("x1", "x2", "x3"), "wald", family = Gamma())
dw <- d[1:45, ]; dw$w <- sample(1:4, 45, replace = TRUE)
nested_case("gamma_log_f_freq", dw, "x1", c("x1", "x2", "x3"), "f", family = Gamma(link = "log"), kind = "frequency")

d <- nested_design(120)
mu <- exp(eta_of(d, c(0.8, 0.5, -0.3, 0.25)))
d$y <- rnbinom(120, size = 2, mu = mu)
nested_case("negbin_lrt", d, "x1", c("x1", "x2", "x3"), "lrt", family = "negbin")
nested_case("negbin_wald", d, "x1", c("x1", "x2", "x3"), "wald", family = "negbin")
nested_case("negbin_fixed_lrt", d, "x1", c("x1", "x2", "x3"), "lrt", family = negative.binomial(1.7), theta = 1.7, dispersion = "fixed")
nested_case("negbin_fixed_f", d, "x1", c("x1", "x2", "x3"), "f", family = negative.binomial(1.7), theta = 1.7, dispersion = "estimated")

# Ordinal comparisons (U68 E5-S1, #653): two REG_ORDINAL (MASS::polr,
# logistic) fits on the same rows, drawn AFTER every case above under
# their own seed, so no case's rows move. Each fit is polr polished to
# its MLE by Newton on the analytic gradient and Hessian (gen_ordinal.R's
# reasoning: polr's BFGS optimum sits ~1e-6 off and its vcov is a
# numerical Hessian), and the script stops unless polr agrees with the
# polished point to 1e-6.
#   lrt   anova.polr(m0, m1): LR stat. = the deviance change -2 ll0 + 2 ll1
#         on chi-square(added predictors); the polished closed form must
#         agree with anova's to 1e-6. Resid. df = sum(w) - edf.
#   wald  t(b_S) solve(V_SS) b_S on chi-square(q) over the full fit's
#         tested predictors, V the inverse analytic information (or the
#         hc1 sandwich H^-1 (sum w s s') H^-1 n/(n - k)); the same Wald on
#         vcov(polr) / sandwich::sandwich(polr, adjust = TRUE) must agree
#         to 1e-4 (polr's numerical Hessian).
# Frequency weights are polr(weights = w), asserted equal to the
# rep()-expanded fit. The rows are [x1, x2, x3, y, w], y the 1-based level.
set.seed(20261117)
ordn_parts <- function(theta, X, y, w, J) {
  p <- ncol(X)
  beta <- theta[seq_len(p)]
  zeta <- theta[p + seq_len(J - 1)]
  eta <- drop(X %*% beta)
  up <- y < J
  lo <- y > 1
  a <- ifelse(up, c(zeta, 0)[y] - eta, 0)
  b <- ifelse(lo, c(0, zeta)[y] - eta, 0)
  fp <- function(x) dlogis(x) * (1 - 2 * plogis(x))
  Fa <- ifelse(up, plogis(a), 1); Fb <- ifelse(lo, plogis(b), 0)
  fa <- ifelse(up, dlogis(a), 0); fb <- ifelse(lo, dlogis(b), 0)
  fpa <- ifelse(up, fp(a), 0); fpb <- ifelse(lo, fp(b), 0)
  pr <- Fa - Fb
  q <- p + J - 1
  S <- matrix(0, length(y), q)
  H <- matrix(0, q, q)
  g <- (fa - fb) / pr
  for (i in seq_along(y)) {
    s <- numeric(q)
    s[seq_len(p)] <- -g[i] * X[i, ]
    h <- matrix(0, q, q)
    h[seq_len(p), seq_len(p)] <- ((fpa[i] - fpb[i]) / pr[i] - g[i]^2) * tcrossprod(X[i, ])
    if (up[i]) {
      k <- p + y[i]
      s[k] <- fa[i] / pr[i]
      h[k, k] <- fpa[i] / pr[i] - (fa[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (-fpa[i] / pr[i] + fa[i] * g[i] / pr[i])
    }
    if (lo[i]) {
      k <- p + y[i] - 1
      s[k] <- -fb[i] / pr[i]
      h[k, k] <- -fpb[i] / pr[i] - (fb[i] / pr[i])^2
      h[seq_len(p), k] <- h[k, seq_len(p)] <- X[i, ] * (fpb[i] / pr[i] - fb[i] * g[i] / pr[i])
    }
    if (up[i] && lo[i]) {
      ku <- p + y[i]; kl <- ku - 1
      h[ku, kl] <- h[kl, ku] <- fa[i] * fb[i] / pr[i]^2
    }
    S[i, ] <- s
    H <- H + w[i] * h
  }
  list(ll = sum(w * log(pr)), S = S, grad = colSums(w * S), H = H)
}
# ordn_fit is polr(yf ~ preds, weights = w) and its polished MLE: theta
# (predictors, then thresholds), -2 ll, the residual df sum(w) - q, the
# classical and hc1 covariances.
ordn_fit <- function(name, d, preds, J) {
  X <- as.matrix(d[, preds, drop = FALSE])
  dd <- data.frame(yf = factor(d$y, levels = seq_len(J)), d[, preds, drop = FALSE], .w = d$w)
  f <- as.formula(paste("yf ~", paste(preds, collapse = " + ")))
  pf <- suppressWarnings(polr(f, data = dd, weights = .w, Hess = TRUE, control = list(reltol = 1e-14, maxit = 5000)))
  theta <- c(coef(pf), pf$zeta)
  for (it in 1:100) {
    pt <- ordn_parts(theta, X, d$y, d$w, J)
    step <- solve(pt$H, pt$grad)
    theta <- theta - step
    if (max(abs(step) / pmax(abs(theta), 1)) < 1e-15) break
  }
  if (it == 100) stop(name, ": Newton polish did not converge")
  # Against max(|want|, 1): polr's optimum is ~1e-6 off in absolute terms.
  err <- max(abs(c(coef(pf), pf$zeta) - theta) / pmax(abs(theta), 1))
  if (err > 1e-6) stop(name, ": polr vs polished MLE ", err)
  pt <- ordn_parts(theta, X, d$y, d$w, J)
  if (max(abs(pt$grad)) > 1e-9) stop(name, ": gradient not zero at the polished point")
  Hinv <- solve(-pt$H)
  n <- sum(d$w); q <- length(theta)
  hc1 <- Hinv %*% crossprod(sqrt(d$w) * pt$S) %*% Hinv * n / (n - q)
  names(theta) <- c(preds, paste0(seq_len(J - 1), "|", seq_len(J - 1) + 1))
  nested_check(paste(name, "polr df.residual"), pf$df.residual, n - q, 1e-12)
  list(pf = pf, theta = theta, dev = -2 * pt$ll, rdf = n - q, V = Hinv, hc1 = hc1)
}
ordinal_cases <- list()
ordn_case <- function(name, d, restricted, full, test, covariance = "", kind = "") {
  if (kind == "") d$w <- 1
  J <- max(d$y)
  if (!all(sort(unique(d$y)) == seq_len(J))) stop(name, ": empty level")
  m0 <- ordn_fit(paste(name, "m0"), d, restricted, J)
  m1 <- ordn_fit(paste(name, "m1"), d, full, J)
  if (kind == "frequency") {
    ex <- d[rep(seq_len(nrow(d)), d$w), ]; ex$w <- 1
    nested_check(paste(name, "weighted vs expanded"), ordn_fit(paste(name, "expanded"), ex, full, J)$theta, m1$theta, 1e-9)
  }
  terms <- setdiff(full, restricted)
  q <- length(terms)
  disp <- NULL
  if (test == "lrt") {
    stat <- m0$dev - m1$dev
    a <- anova(m0$pf, m1$pf)
    nested_check(paste(name, "anova.polr LR stat."), a$`LR stat.`[2], stat, 1e-6)
    nested_check(paste(name, "anova.polr df"), a$`   Df`[2], q, 1e-12)
    p <- pchisq(stat, q, lower.tail = FALSE)
    nested_check(paste(name, "anova.polr p"), a$`Pr(Chi)`[2], p, 1e-5)
    disp <- 1
  } else {
    V <- if (covariance == "hc1") m1$hc1 else m1$V
    b <- m1$theta[terms]
    idx <- match(terms, names(m1$theta))
    stat <- as.numeric(t(b) %*% solve(V[idx, idx, drop = FALSE], b))
    Vr <- if (covariance == "hc1") sandwich(m1$pf, adjust = TRUE) else vcov(m1$pf)
    br <- coef(m1$pf)[terms]
    nested_check(paste(name, "Wald on polr's vcov"), as.numeric(t(br) %*% solve(Vr[terms, terms, drop = FALSE], br)), stat, 1e-4)
    p <- pchisq(stat, q, lower.tail = FALSE)
  }
  rows <- lapply(seq_len(nrow(d)), function(i) nested_g17(c(d$x1[i], d$x2[i], d$x3[i], d$y[i], d$w[i])))
  case <- list(name = name, test = test, n = nrow(d), family = "ordinal", link = "logit", theta = 0, dispersion = "",
    covariance = covariance, weight = kind, restricted = I(restricted), full = I(full), restricted_intercept = FALSE,
    terms = I(terms), distribution = "chisq", rows = rows,
    statistic = nested_num(stat), df = nested_num(q), p_value = nested_num(p),
    restricted_df_residual = nested_num(m0$rdf), full_df_residual = nested_num(m1$rdf),
    restricted_deviance = nested_num(m0$dev), full_deviance = nested_num(m1$dev),
    dispersion_used = nested_num(disp))
  ordinal_cases[[length(ordinal_cases) + 1]] <<- Filter(Negate(is.null), case)
}
# ordn_draw: n rows, a J-level response from the latent x'beta + logistic.
ordn_draw <- function(n, beta, cuts) {
  d <- nested_design(n)
  ys <- drop(as.matrix(d[, c("x1", "x2", "x3")]) %*% beta) + rlogis(n)
  d$y <- findInterval(ys, cuts) + 1
  d$w <- 1
  d
}
d <- ordn_draw(220, c(0.9, -0.6, 0.35), c(-0.5, 0.8))
ordn_case("ordinal_lrt", d, "x1", c("x1", "x2", "x3"), "lrt")
ordn_case("ordinal_wald", d, "x1", c("x1", "x2", "x3"), "wald")
ordn_case("ordinal_wald_hc1", d, c("x1", "x2"), c("x1", "x2", "x3"), "wald", covariance = "hc1")
d <- ordn_draw(90, c(0.7, -0.4, 0.5), c(-1, 0, 1.1))
d$w <- sample(1:4, 90, replace = TRUE)
ordn_case("ordinal_lrt_freq", d, "x1", c("x1", "x2", "x3"), "lrt", kind = "frequency")

# Multinomial comparisons (U68 E5-S2, D7): two REG_MULTINOMIAL
# (nnet::multinom) fits on the same rows, drawn AFTER every case above
# under their own seed, so no case's rows move. Each fit is multinom
# polished to its MLE by Newton on the analytic gradient and Hessian
# (gen_multinomial.R's reasoning: multinom's BFGS stops ~1e-8 off and its
# vcov is a finite-difference Hessian); the script stops unless multinom
# agrees with the polished point to 1e-6. With K non-reference levels
# and q added predictors, every test reads the K q "<level>:<term>"
# coefficients of the added predictors:
#   lrt   anova(m0, m1) (anova.multinom): LR stat. = the deviance change
#         -2 ll0 + 2 ll1 on chi-square(K q); the polished closed form
#         must agree with anova's to 1e-5 and its Df exactly. Resid. df
#         = K N* - edf, edf = K (p + 1) — anova's nrow (L - 1) - edf, N*
#         the rep() expansion's sum(w) under a frequency weight (anova on
#         the expanded fits is the check).
#   wald  t(b_S) solve(V_SS) b_S on chi-square(K q) over every key of the
#         added predictors, V the inverse analytic information (or the
#         hc1 sandwich B M B N*/(N* - d), gen_multinomial.R's mn_robust);
#         the classical Wald on vcov(multinom) must agree to 1e-4.
# Frequency weights are multinom(weights = w), asserted equal to the
# rep()-expanded fit. A named reference reorders the fitted levels
# (reference first). The rows are [x1, x2, x3, y, w], y the 1-based
# index of the row's level in `levels` (the target's dictionary order).
set.seed(20261118)
mnst_parts <- function(beta, X, Yi, w) {
  k <- ncol(Yi) - 1; q <- ncol(X)
  B <- matrix(beta, nrow = q)
  eta <- cbind(0, X %*% B)
  m <- apply(eta, 1, max)
  e <- exp(eta - m)
  P <- e / rowSums(e)
  ll <- sum(w * (rowSums(Yi * eta) - m - log(rowSums(e))))
  g <- as.vector(t(X) %*% (w * (Yi[, -1, drop = FALSE] - P[, -1, drop = FALSE])))
  H <- matrix(0, k * q, k * q)
  for (a in 1:k) for (b in 1:k) {
    s <- if (a == b) P[, a + 1] * (1 - P[, a + 1]) else -P[, a + 1] * P[, b + 1]
    H[(a - 1) * q + 1:q, (b - 1) * q + 1:q] <- t(X) %*% (X * (w * s))
  }
  list(P = P, ll = ll, g = g, H = H)
}
# mnst_fit is multinom(yy ~ preds, weights = w) and its polished MLE:
# beta (category-major over the non-reference levels, (intercept) then
# preds), the keys, -2 ll, the residual df K sum(w) - d, the classical
# and hc1 covariances.
mnst_fit <- function(name, d, preds, levels, ref) {
  observed <- levels[levels %in% levels[d$y]]
  fitted_levels <- c(ref, setdiff(observed, ref))
  yy <- factor(levels[d$y], levels = fitted_levels)
  dd <- data.frame(d[, preds, drop = FALSE], yy = yy, .w = d$w)
  f <- as.formula(paste("yy ~", paste(preds, collapse = " + ")))
  m <- suppressWarnings(multinom(f, data = dd, weights = .w, Hess = TRUE, maxit = 1000, reltol = 1e-16, trace = FALSE))
  X <- cbind(1, as.matrix(d[, preds, drop = FALSE]))
  Yi <- sapply(fitted_levels, function(l) as.numeric(as.character(yy) == l))
  beta <- as.vector(t(coef(m)))
  for (it in 1:100) {
    pp <- mnst_parts(beta, X, Yi, d$w)
    step <- solve(pp$H, pp$g)
    beta <- beta + step
    if (max(abs(step)) <= 1e-15 * max(1, max(abs(beta)))) break
  }
  if (it == 100) stop(name, ": Newton polish did not converge")
  pp <- mnst_parts(beta, X, Yi, d$w)
  if (max(abs(pp$g)) > 1e-9) stop(name, ": gradient not zero at the polished MLE")
  err <- max(abs(as.vector(t(coef(m))) - beta) / pmax(abs(beta), 1))
  if (err > 1e-6) stop(name, ": multinom vs polished MLE ", err)
  V <- solve(pp$H)
  k <- length(fitted_levels) - 1
  keys <- as.vector(t(outer(fitted_levels[-1], c("(intercept)", preds), paste, sep = ":")))
  R <- Yi[, -1, drop = FALSE] - pp$P[, -1, drop = FALSE]
  S <- do.call(cbind, lapply(seq_len(k), function(j) X * R[, j]))
  nstar <- sum(d$w); dd_ <- length(beta)
  hc1 <- V %*% crossprod(S, S * d$w) %*% V * (nstar / (nstar - dd_))
  names(beta) <- keys
  dimnames(V) <- dimnames(hc1) <- list(keys, keys)
  list(m = m, beta = beta, k = k, dev = -2 * pp$ll, rdf = k * nstar - dd_, V = V, hc1 = hc1,
    fitted_levels = fitted_levels)
}
multinomial_cases <- list()
mnst_case <- function(name, d, levels, restricted, full, test, covariance = "", kind = "", reference = "") {
  if (kind == "") d$w <- 1
  ref <- if (reference == "") levels[levels %in% levels[d$y]][1] else reference
  m0 <- mnst_fit(paste(name, "m0"), d, restricted, levels, ref)
  m1 <- mnst_fit(paste(name, "m1"), d, full, levels, ref)
  terms <- setdiff(full, restricted)
  q <- m1$k * length(terms)
  disp <- NULL
  if (kind == "frequency") {
    ex <- d[rep(seq_len(nrow(d)), d$w), ]; ex$w <- 1
    e0 <- mnst_fit(paste(name, "expanded m0"), ex, restricted, levels, ref)
    e1 <- mnst_fit(paste(name, "expanded m1"), ex, full, levels, ref)
    nested_check(paste(name, "weighted vs expanded"), e1$beta, m1$beta, 1e-9)
    a_m0 <- e0$m; a_m1 <- e1$m
  } else {
    a_m0 <- m0$m; a_m1 <- m1$m
  }
  a <- anova(a_m0, a_m1)
  nested_check(paste(name, "anova.multinom Resid. df"), a$`Resid. df`, c(m0$rdf, m1$rdf), 1e-12)
  nested_check(paste(name, "anova.multinom Df"), a$`   Df`[2], q, 1e-12)
  if (test == "lrt") {
    stat <- m0$dev - m1$dev
    nested_check(paste(name, "anova.multinom LR stat."), a$`LR stat.`[2], stat, 1e-5)
    disp <- 1
  } else {
    idx <- which(sub("^.*:", "", names(m1$beta)) %in% terms)
    if (length(idx) != q) stop(name, ": tested keys")
    V <- if (covariance == "hc1") m1$hc1 else m1$V
    b <- m1$beta[idx]
    stat <- as.numeric(t(b) %*% solve(V[idx, idx, drop = FALSE], b))
    if (covariance == "") {
      Vr <- vcov(m1$m)
      br <- as.vector(t(coef(m1$m)))[idx]
      nested_check(paste(name, "Wald on vcov(multinom)"), as.numeric(t(br) %*% solve(Vr[idx, idx, drop = FALSE], br)), stat, 1e-4)
    }
  }
  p <- pchisq(stat, q, lower.tail = FALSE)
  rows <- lapply(seq_len(nrow(d)), function(i) nested_g17(c(d$x1[i], d$x2[i], d$x3[i], d$y[i], d$w[i])))
  case <- list(name = name, test = test, n = nrow(d), family = "multinomial", link = "logit", theta = 0, dispersion = "",
    covariance = covariance, weight = kind, levels = I(levels), reference = reference,
    restricted = I(restricted), full = I(full), restricted_intercept = TRUE,
    terms = I(terms), distribution = "chisq", rows = rows,
    statistic = nested_num(stat), df = nested_num(q), p_value = nested_num(p),
    restricted_df_residual = nested_num(m0$rdf), full_df_residual = nested_num(m1$rdf),
    restricted_deviance = nested_num(m0$dev), full_deviance = nested_num(m1$dev),
    dispersion_used = nested_num(disp))
  multinomial_cases[[length(multinomial_cases) + 1]] <<- Filter(Negate(is.null), case)
}
# mnst_draw: n rows, a response over length(levels) levels from the
# baseline-category logit with intercepts a and slope rows B (one per
# non-first level) over [x1, x2, x3]; one inverse-CDF uniform per row.
mnst_draw <- function(n, a, B) {
  d <- nested_design(n)
  eta <- cbind(0, sweep(as.matrix(d[, c("x1", "x2", "x3")]) %*% t(B), 2, a, "+"))
  P <- exp(eta - apply(eta, 1, max)); P <- P / rowSums(P)
  u <- runif(n)
  d$y <- 1 + rowSums(u > t(apply(P, 1, cumsum)))
  d$w <- 1
  d
}
d <- mnst_draw(240, c(0.3, -0.2), rbind(c(0.8, -0.5, 0.3), c(-0.4, 0.7, 0.25)))
mnst_case("multinomial_lrt", d, c("lo", "mid", "hi"), "x1", c("x1", "x2", "x3"), "lrt")
mnst_case("multinomial_wald", d, c("lo", "mid", "hi"), "x1", c("x1", "x2", "x3"), "wald")
mnst_case("multinomial_wald_hc1", d, c("lo", "mid", "hi"), c("x1", "x2"), c("x1", "x2", "x3"), "wald", covariance = "hc1")
d <- mnst_draw(110, c(0.2, -0.3, 0.1), rbind(c(0.6, -0.4, 0.2), c(-0.5, 0.5, 0.3), c(0.3, 0.3, -0.6)))
d$w <- sample(1:3, 110, replace = TRUE)
mnst_case("multinomial_lrt_freq_ref", d, c("q1", "q2", "q3", "q4"), "x1", c("x1", "x2"), "lrt", kind = "frequency", reference = "q3")

write_golden("nested", list(
  generator = "scripts/reference/gen_nested.R",
  r_version = R.version.string,
  packages = as.list(sapply(names(nested_pins), function(p) as.character(packageVersion(p)))),
  note = paste(
    "restricted y ~ restricted (through the origin unless restricted_intercept) vs full y ~ full;",
    "test f = anova(m0, m1[, test = 'F']); lrt = anova(m0, m1, test = 'Chisq' | 'LRT') (statistic = deviance change / dispersion_used),",
    "a theta-estimated negative binomial's = 2 loglik change (anova.negbin); wald = lmtest::waldtest(m0, m1, vcov = V, test = 'F' | 'Chisq'),",
    "V = vcov(m1) or vcovHC(m1, type = 'HC3'). rows [x1, x2, x3, y, w]; weight '' (w = 1) or frequency (fitted on the rep() expansion)."),
  cases = nested_cases,
  ordinal_note = paste(
    "REG_ORDINAL (MASS::polr, logistic) restricted yf ~ restricted vs full yf ~ full, each polished to its MLE;",
    "lrt = anova.polr (statistic = deviance change, df = added predictors, dispersion_used 1);",
    "wald = t(b_S) solve(V_SS) b_S, V the inverse analytic information or the hc1 sandwich, chi-square on q.",
    "rows [x1, x2, x3, y, w], y the 1-based level; weight frequency = polr(weights = w)."),
  ordinal_cases = ordinal_cases,
  multinomial_note = paste(
    "REG_MULTINOMIAL (nnet::multinom) restricted yy ~ restricted vs full yy ~ full, each polished to its MLE, the reference",
    "`reference` (or the first observed level of `levels`); lrt = anova.multinom (statistic = deviance change, df = (L - 1) x added",
    "predictors, Resid. df = (L - 1) N* - edf, dispersion_used 1); wald = t(b_S) solve(V_SS) b_S over every '<level>:<term>' key of",
    "the added predictors, V the inverse analytic information or the hc1 sandwich, chi-square on the same df.",
    "rows [x1, x2, x3, y, w], y the 1-based index into levels; weight frequency = multinom(weights = w) (anova on the rep() expansion)."),
  multinomial_cases = multinomial_cases
))
