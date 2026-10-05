# Frequency-weight reference for the weighted regressions, regression
# attributes (weighting-inferential E4-S3, .claude/reference/weighting.md
# "Weighted inference"), z / t score attributes and quantile cuts (E5-S2).
# Called by gen_weight_reference.py — never by CI.
#
# Base R only (stats): every figure is the STOCK R function run on the
# rep()-expanded rows, the definition of a frequency weight — lm() for
# REG_OLS and the ATTR_REG_* fitted values / residuals / leverages, and
# glm(control = glm.control(epsilon = 1e-14, maxit = 100)) with
# summary(dispersion = 1) for REG_GLM (the engine fixes the dispersion
# at 1, the gamma caveat included; gamma uses its default inverse link),
# refit once from its own converged coefficients (see below).
# Pinned: R 4.6.1 (refuses any other version).
#
# Usage: Rscript reg_reference.R <csv>
#   csv columns id,x1,x2,y,yb,yp,yg,f — x2 may be NA; f the row's copy
#   count (0 for a zero or invalid frequency weight: the row is not in
#   the fit, but the attributes still evaluate it).
# Prints one line "R <ver>", then "<case> <figure> <value>" lines at 17
# significant digits.
want <- "4.6.1"
have <- sprintf("%s.%s", R.version$major, R.version$minor)
if (have != want) stop(sprintf("R %s required, found %s", want, have))
args <- commandArgs(trailingOnly = TRUE)
if (length(args) != 1) stop("usage: reg_reference.R <csv>")
d <- read.csv(args[[1]], stringsAsFactors = FALSE)
d$row <- seq_len(nrow(d))
copies <- ifelse(is.na(d$x2), 0L, d$f)
e <- d[rep(d$row, copies), ]

cat(sprintf("R %s\n", have))
out <- function(case, fig, v) cat(sprintf("%s %s %.17g\n", case, fig, v))
terms <- c("(intercept)", "x1", "x2")

# REG_OLS: lm on the expansion.
fit <- lm(y ~ x1 + x2, data = e)
s <- summary(fit)
for (i in 1:3) {
  out("ols", sprintf("coefficients.%s", terms[i]), s$coefficients[i, 1])
  out("ols", sprintf("std_errors.%s", terms[i]), s$coefficients[i, 2])
  out("ols", sprintf("p_values.%s", terms[i]), s$coefficients[i, 4])
}
out("ols", "r2", s$r.squared)
out("ols", "adj_r2", s$adj.r.squared)
out("ols", "residual_std_err", s$sigma)

# ATTR_REG_FITTED / RESIDUAL / LEVERAGE: every row with both predictors
# gets the expanded fit's prediction; a row in the fit gets f × one
# copy's hat value (its copies share the row's hat mass), any other row
# 0. A row with a null predictor emits 0 throughout.
hat <- hatvalues(fit)
first <- match(d$row, e$row)
pred <- predict(fit, newdata = d)
for (r in d$row) {
  id <- d$id[r]
  if (is.na(d$x2[r])) {
    fv <- 0; rv <- 0; lv <- 0
  } else {
    fv <- pred[r]
    rv <- d$y[r] - pred[r]
    lv <- if (copies[r] > 0) copies[r] * hat[first[r]] else 0
  }
  out("attr_fitted", id, fv)
  out("attr_residual", id, rv)
  out("attr_leverage", id, lv)
}

# REG_GLM: glm on the expansion, dispersion fixed at 1.
fams <- list(glm_binomial = list(binomial(), "yb"),
             glm_poisson = list(poisson(), "yp"),
             glm_gamma = list(Gamma(), "yg"))
for (nm in names(fams)) {
  e$target <- e[[fams[[nm]][[2]]]]
  g <- glm(target ~ x1 + x2, family = fams[[nm]][[1]], data = e,
           control = glm.control(epsilon = 1e-14, maxit = 100))
  if (!g$converged) stop(sprintf("%s did not converge", nm))
  # glm's standard errors use the IRLS weights of the LAST iteration's
  # starting point, ~1e-8 from the converged fit's at epsilon 1e-14: one
  # warm-started refit from the converged coefficients evaluates them at
  # the MLE itself.
  g <- glm(target ~ x1 + x2, family = fams[[nm]][[1]], data = e, start = coef(g),
           control = glm.control(epsilon = 1e-14, maxit = 100))
  if (!g$converged) stop(sprintf("%s refit did not converge", nm))
  gs <- summary(g, dispersion = 1)
  for (i in 1:3) {
    out(nm, sprintf("coefficients.%s", terms[i]), gs$coefficients[i, 1])
    out(nm, sprintf("std_errors.%s", terms[i]), gs$coefficients[i, 2])
    out(nm, sprintf("p_values.%s", terms[i]), gs$coefficients[i, 4])
  }
  out(nm, "deviance", deviance(g))
  out(nm, "null_deviance", g$null.deviance)
}

# ATTR_ZSCORE / ATTR_TSCORE (weighting-inferential E5-S2): every row's
# score against the mean and POPULATION sd (divisor n) of the expanded
# y — all rows expand here (x2 is irrelevant to a score); a row with no
# copies is still scored.
es <- d[rep(d$row, d$f), ]
m <- mean(es$y)
s <- sqrt(sum((es$y - m)^2) / nrow(es))
for (r in d$row) {
  z <- (d$y[r] - m) / s
  out("attr_zscore", d$id[r], z)
  out("attr_tscore", d$id[r], z * 10 + 50)
}

# GROUP_QUANTILE: the order statistic that opens each of k buckets on
# the expanded yg — 0-based expanded index ceiling(b·W/k), b = 0..k−1
# (W the expanded row count), i.e. the first expanded row whose
# floor(rank·k/W) is b.
ys <- sort(es$yg)
for (k in c(4, 10)) {
  for (b in 0:(k - 1)) {
    out(sprintf("group_quantile_k%d", k), sprintf("cut%d", b), ys[ceiling(b * nrow(es) / k) + 1])
  }
}
