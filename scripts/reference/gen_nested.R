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
# negative.binomial, anova.negbin) + sandwich 3.1-3 (vcovHC) + lmtest
# 0.9-40 (waldtest, lrtest; with zoo 1.9-1), installed from the Posit
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
nested_pins <- c(lmtest = "0.9.40", zoo = "1.9.1", sandwich = "3.1.3", MASS = "7.3.66", jsonlite = "2.0.0")
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

write_golden("nested", list(
  generator = "scripts/reference/gen_nested.R",
  r_version = R.version.string,
  packages = as.list(sapply(names(nested_pins), function(p) as.character(packageVersion(p)))),
  note = paste(
    "restricted y ~ restricted (through the origin unless restricted_intercept) vs full y ~ full;",
    "test f = anova(m0, m1[, test = 'F']); lrt = anova(m0, m1, test = 'Chisq' | 'LRT') (statistic = deviance change / dispersion_used),",
    "a theta-estimated negative binomial's = 2 loglik change (anova.negbin); wald = lmtest::waldtest(m0, m1, vcov = V, test = 'F' | 'Chisq'),",
    "V = vcov(m1) or vcovHC(m1, type = 'HC3'). rows [x1, x2, x3, y, w]; weight '' (w = 1) or frequency (fitted on the rep() expansion)."),
  cases = nested_cases
))
