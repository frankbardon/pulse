# gen_vif.R — R oracle for the variance inflation factors a REG_OLS /
# REG_GLM fit reports (RegressionSpec.Collinearity, U65 E3-S2, #628):
# per term the VIF, Fox & Monette's generalized VIF for a multi-column
# term (a factor's dummies), its df and GVIF^(1/(2 df)), on the model
# basis (car::vif on the fit) and the predictors basis (car::vif on lm
# over the same predictors and prior weights).
#
# Writes <out_dir>/vif.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   R_LIBS_USER=... Rscript scripts/reference/gen_vif.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite 2.0.0 + MASS 7.3-66 (negative.binomial) + car 3.1-5
# (vif; with carData 3.0-6, installed from the Posit noble binary repo;
# versions recorded in the golden).
#
# The reference is car::vif (vif.default: GVIF = det(R_SS) det(R_-S,-S) /
# det(R), R = cov2cor(vcov(m)) without the intercept, terms from the
# model's assign; car stops below two terms). The script re-derives each
# figure from its closed form and stops on a mismatch:
#   model basis       R = the correlation of the centered X'WX, W =
#                     m$weights (the working weights at the fit; the
#                     prior weights on lm) — so car's vcov-based figure is
#                     a property of the weighted design, phi aside
#   predictors basis  car::vif(lm(y ~ same terms, weights = prior))
# Frequency-weighted cases ("_freq", integer f) equal the rep()-expanded
# fit's figures (asserted); probability-weighted ones ("_prob",
# fractional w) are glm / lm(weights = w) — scale-free, so Pulse's w* =
# w N*/sum(w) gives the same figures.
#
# GLM fits are refitted from their own optimum until the coefficients
# stop moving (glm.control(epsilon = 1e-14)), so the stored working
# weights are the converged ones.
#
# Seeded (set.seed(20261112)); draws its own rows, so no other golden
# moves. Rows are [x1, x2, x3, gb, gc, y, w] (gb, gc the treatment
# dummies of a three-level factor g, baseline a; w = 1 unweighted).

suppressPackageStartupMessages(library(jsonlite))
suppressPackageStartupMessages(library(MASS))
suppressPackageStartupMessages(library(car))

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_vif.R pins R 4.6.1, got ", R.version.string)
vif_pins <- c(car = "3.1.5", carData = "3.0.6", MASS = "7.3.66", jsonlite = "2.0.0")
for (p in names(vif_pins)) {
  if (as.character(packageVersion(p)) != vif_pins[[p]]) stop("gen_vif.R pins ", p, " ", vif_pins[[p]], ", got ", packageVersion(p))
}

vif_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
vif_check <- function(what, got, want, tol = 1e-11) {
  got <- as.numeric(got); want <- as.numeric(want)
  if (length(got) != length(want)) stop(what, ": length mismatch")
  if (max(abs(got - want) / pmax(1e-300, abs(want))) > tol) stop(what, ": closed form disagrees with car::vif")
}
vif_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# vif_fit fits formula f on d (weights column .w) as lm (family NULL) or
# glm, a glm refitted to its fixed point.
vif_fit <- function(f, d, family) {
  if (is.null(family)) return(lm(f, data = d, weights = .w))
  m <- suppressWarnings(glm(f, family = family, data = d, weights = .w, control = vif_ctl))
  for (k in 1:50) {
    b <- coef(m)
    m <- suppressWarnings(glm(f, family = family, data = d, weights = .w, control = vif_ctl, start = b))
    if (max(abs(coef(m) - b) / pmax(abs(b), 1e-300)) <= 1e-15) break
  }
  if (!m$converged) stop("glm did not converge")
  m
}

# vif_table is car::vif(m) as (gvif, df, scaled) per term.
vif_table <- function(m) {
  v <- vif(m)
  if (is.null(dim(v))) return(list(gvif = unname(v), df = rep(1, length(v)), scaled = sqrt(unname(v)), terms = names(v)))
  list(gvif = unname(v[, 1]), df = unname(v[, 2]), scaled = unname(v[, 3]), terms = rownames(v))
}

# vif_closed is the generalized VIF of each term from the centered
# W-weighted cross-product of the model matrix (no intercept).
vif_closed <- function(m, w) {
  X <- model.matrix(m)
  assign <- attr(X, "assign")[-1]
  X <- X[, -1, drop = FALSE]
  mu <- colSums(w * X) / sum(w)
  Xc <- sweep(X, 2, mu)
  R <- cov2cor(crossprod(Xc * sqrt(w)))
  sapply(sort(unique(assign)), function(t) {
    s <- which(assign == t)
    det(R[s, s, drop = FALSE]) * det(R[-s, -s, drop = FALSE]) / det(R)
  })
}

vif_cases <- list()

# vif_case records one fit. family NULL is lm; grouped uses the factor g
# (the dummies gb, gc as one term), else x3; basis model or predictors;
# kind "" (unweighted), "frequency" (d$.w integer) or "probability".
vif_case <- function(name, d, family = NULL, grouped = FALSE, basis = "model", kind = "", theta = 0) {
  f <- if (grouped) y ~ x1 + g + x2 else y ~ x1 + x2 + x3
  if (kind == "") d$.w <- 1
  m <- vif_fit(f, d, family)
  ref <- if (basis == "model") m else lm(f, data = d, weights = .w)
  tab <- vif_table(ref)
  w <- if (basis == "model" && !is.null(family)) m$weights else d$.w
  vif_check(paste(name, "closed form"), vif_closed(m, w), tab$gvif)
  if (kind == "frequency") {
    ex <- d[rep(seq_len(nrow(d)), d$.w), ]
    ex$.w <- 1
    mx <- vif_fit(f, ex, family)
    rx <- if (basis == "model") mx else lm(f, data = ex, weights = .w)
    vif_check(paste(name, "weighted vs expanded"), vif_table(rx)$gvif, tab$gvif, 1e-9)
  }
  predictors <- if (grouped) c("x1", "gb", "gc", "x2") else c("x1", "x2", "x3")
  terms <- if (grouped) c("x1", "g", "x2") else c("x1", "x2", "x3")
  if (!identical(tab$terms, terms)) stop(name, ": car terms ", paste(tab$terms, collapse = ","))
  fam <- if (is.null(family)) list(family = "gaussian", link = "identity") else family
  famname <- if (grepl("^Negative Binomial", fam$family)) "negative_binomial" else tolower(fam$family)
  rows <- lapply(seq_len(nrow(d)), function(i) {
    vif_g17(c(d$x1[i], d$x2[i], d$x3[i], as.numeric(d$g[i] == "b"), as.numeric(d$g[i] == "c"), d$y[i], d$.w[i]))
  })
  vif_cases[[length(vif_cases) + 1]] <<- list(
    name = name, family = famname, link = fam$link, theta = theta, basis = basis, weight = kind,
    grouped = grouped, predictors = predictors, terms = terms, rows = rows,
    gvif = vif_g17(tab$gvif), df = vif_g17(tab$df), gvif_scaled = vif_g17(tab$scaled))
}

set.seed(20261112)
# vif_design draws n rows: x2 leans on x1, x3 on both, g on x1 — so every
# VIF sits well above 1.
vif_design <- function(n) {
  x1 <- round(rnorm(n), 4)
  x2 <- round(0.6 * x1 + rnorm(n, sd = 0.8), 4)
  x3 <- round(0.4 * x1 - 0.5 * x2 + rnorm(n, sd = 0.7), 4)
  u <- 0.8 * x1 + rnorm(n)
  g <- factor(ifelse(u < -0.4, "a", ifelse(u < 0.5, "b", "c")), levels = c("a", "b", "c"))
  data.frame(x1 = x1, x2 = x2, x3 = x3, g = g)
}
eta_of <- function(d, b) b[1] + b[2] * d$x1 + b[3] * d$x2 + b[4] * d$x3 + b[5] * (d$g == "b") + b[6] * (d$g == "c")

d <- vif_design(80)
d$y <- round(eta_of(d, c(1, 0.8, -0.5, 0.3, 0.4, -0.6)) + rnorm(80, sd = 0.9), 4)
vif_case("ols", d)
vif_case("ols_group", d, grouped = TRUE)
dp <- d; dp$.w <- round(runif(80, 0.2, 3), 3)
vif_case("ols_prob", dp, kind = "probability")
dw <- d[1:40, ]; dw$.w <- sample(1:4, 40, replace = TRUE)
vif_case("ols_group_freq", dw, grouped = TRUE, kind = "frequency")

d <- vif_design(120)
d$y <- rbinom(120, 1, plogis(eta_of(d, c(-0.2, 0.9, -0.6, 0.5, 0.5, -0.4))))
vif_case("logit", d, family = binomial())
vif_case("logit_group", d, family = binomial(), grouped = TRUE)
vif_case("logit_group_predictors", d, family = binomial(), grouped = TRUE, basis = "predictors")
dw <- d[1:60, ]; dw$.w <- sample(1:4, 60, replace = TRUE)
vif_case("logit_group_freq", dw, family = binomial(), grouped = TRUE, kind = "frequency")

d <- vif_design(100)
d$y <- rpois(100, exp(eta_of(d, c(0.4, 0.5, -0.3, 0.2, 0.3, -0.3))))
vif_case("poisson_group", d, family = poisson(), grouped = TRUE)
dw <- d[1:50, ]; dw$.w <- sample(1:4, 50, replace = TRUE)
vif_case("poisson_predictors_freq", dw, family = poisson(), basis = "predictors", kind = "frequency")
vif_case("negbin_fixed_theta", d, family = negative.binomial(1.7), theta = 1.7)

d <- vif_design(90)
d$y <- round(rgamma(90, shape = 4, rate = 4 / exp(eta_of(d, c(0.5, 0.3, -0.2, 0.2, 0.2, -0.2)))), 5)
d$.w <- round(runif(90, 0.2, 3), 3)
vif_case("gamma_log_group_prob", d, family = Gamma(link = "log"), grouped = TRUE, kind = "probability")

write_golden("vif", list(
  generator = "scripts/reference/gen_vif.R",
  r_version = R.version.string,
  packages = as.list(sapply(names(vif_pins), function(p) as.character(packageVersion(p)))),
  note = paste(
    "gvif / df / gvif_scaled = car::vif(m) (GVIF, Df, GVIF^(1/(2*Df)); gvif_scaled = sqrt(gvif) when every Df is 1),",
    "m = lm / glm(y ~ x1 + x2 + x3) or (grouped) y ~ x1 + g + x2, g's treatment dummies [gb, gc] one term;",
    "basis model: car::vif on the fit; predictors: car::vif(lm(same terms, weights = prior)).",
    "rows [x1, x2, x3, gb, gc, y, w]; weight '' (w = 1), frequency (= the rep() expansion) or probability."),
  cases = vif_cases
))
