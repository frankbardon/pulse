# gen_test_anova_rm_mixed.R — R oracle for TEST_ANOVA_RM params.incomplete
# "mixed" (U69 E6-S1, #678): the REML random-intercept model
# y ~ cond + (1 | subj) fitted on every observed (subject, condition)
# cell, and the F test of cond with Satterthwaite's denominator df.
#
# Writes <out_dir>/test_anova_rm_mixed.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir, write_golden()
# and num(); runnable on its own too:
#
#   Rscript scripts/reference/gen_test_anova_rm_mixed.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base R
# (stats) + jsonlite, lme4 2.0-6 and lmerTest 3.2-1 (with numDeriv
# 2016.8-1.1).
#
# lmerTest's figures carry its optimiser's and numDeriv's error (the
# REML optimum from bobyqa / nloptwrap, the Hessian and the Jacobian by
# Richardson extrapolation): a few 1e-6 relative. So the golden is the
# quantity lmerTest approximates, computed in closed form on the dense
# N x N matrices, and lmerTest's own anova(m, ddf = "Satterthwaite")
# rides beside as r_lmertest_* (the script stops unless it agrees within
# 1e-4 relative):
#   V(phi)   = phi_s Z Z' + phi_e I, phi = (subject, residual) variance
#   REML     = log|V| + log|X' V^-1 X| + y' P y, P = V^-1 - V^-1 X C X' V^-1,
#              C = (X' V^-1 X)^-1 (the cov of the fixed effects)
#   optimum  phi_e = y' P_H y / (N - p) at gamma = phi_s / phi_e, the root
#            of the analytic gradient tr(P V_s) - y' P V_s P y found by
#            uniroot (tol 1e-15) in gamma; gamma = 0 when the gradient is
#            already >= 0 there (the boundary)
#   A        = 2 H^-1, H the OBSERVED Hessian of REML in phi:
#              H_jk = -tr(P V_j P V_k) + 2 y' P V_j P V_k P y
#              (at the boundary only phi_e is free)
#   dC/dphi_j = C X' V^-1 V_j V^-1 X C
#   F, ddf   lmerTest's contestMD: L picks the cond contrasts, L C L' =
#            P D P', F = sum((P' L beta)^2 / D) / q, nu_m = 2 D_m^2 /
#            (g_m' A g_m), g_mj = P_m' L dC/dphi_j L' P_m; ddf = nu_1 when
#            q = 1 or every nu within 1e-8 of nu_1, 2 when any nu <= 2,
#            else 2E / (E - q), E = sum(nu / (nu - 2)) (get_Fstat_ddf)
#   p        pf(F, q, ddf, lower.tail = FALSE)
# A balanced, interior case also records the classic
# summary(aov(y ~ cond + Error(subj / cond))) F, which the mixed F must
# reproduce. Conditions and subjects are lowercase ASCII, so R's factor
# order is Go's byte order.

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
if (!exists("num")) {
  num <- function(x) {
    if (length(x) != 1) stop("num() takes a scalar")
    if (!is.finite(x)) stop("non-finite reference value: ", x)
    structure(sprintf("%.17g", x), class = "json")
  }
}
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_test_anova_rm_mixed.R pins R 4.6.1, got ", R.version.string)
rmm_pins <- c(lme4 = "2.0.6", lmerTest = "3.2.1", numDeriv = "2016.8.1.1")
for (p in names(rmm_pins)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != rmm_pins[[p]]) stop(sprintf("gen_test_anova_rm_mixed.R pins %s %s, got %s", p, rmm_pins[[p]], have))
}
suppressPackageStartupMessages(library(lmerTest))

rmm_vec <- function(v) lapply(as.numeric(v), num)

rmm_fit <- function(d) {
  conds <- sort(unique(d$cond))
  d$cond <- factor(d$cond, levels = conds)
  d$subj <- factor(d$subj)
  X <- model.matrix(~cond, d)
  y <- d$y
  N <- nrow(X); p <- ncol(X); q <- p - 1
  Z <- model.matrix(~ subj - 1, d)
  ZZ <- Z %*% t(Z)
  Vs <- list(ZZ, diag(N))
  parts <- function(ph) {
    Vi <- solve(ph[1] * ZZ + ph[2] * diag(N))
    C <- solve(t(X) %*% Vi %*% X)
    P <- Vi - Vi %*% X %*% C %*% t(X) %*% Vi
    list(Vi = Vi, C = C, P = P)
  }
  sigma2 <- function(g) drop(t(y) %*% parts(c(g, 1))$P %*% y) / (N - p)
  grad_s <- function(g) {
    o <- parts(c(g, 1))
    sum(diag(o$P %*% ZZ)) - drop(t(y) %*% o$P %*% ZZ %*% o$P %*% y) / sigma2(g)
  }
  if (grad_s(0) >= 0) {
    gamma <- 0
  } else {
    hi <- 1
    while (grad_s(hi) < 0) hi <- hi * 2
    gamma <- uniroot(grad_s, c(0, hi), tol = 1e-15, maxiter = 10000)$root
  }
  s2 <- sigma2(gamma)
  ph <- c(gamma * s2, s2)
  o <- parts(ph)
  beta <- drop(o$C %*% t(X) %*% o$Vi %*% y)
  free <- if (gamma > 0) 1:2 else 2
  H <- matrix(0, length(free), length(free))
  for (a in seq_along(free)) for (b in seq_along(free)) {
    j <- free[a]; k <- free[b]
    H[a, b] <- -sum(diag(o$P %*% Vs[[j]] %*% o$P %*% Vs[[k]])) +
      2 * drop(t(y) %*% o$P %*% Vs[[j]] %*% o$P %*% Vs[[k]] %*% o$P %*% y)
  }
  A <- 2 * solve(H)
  dC <- lapply(free, function(j) o$C %*% t(X) %*% o$Vi %*% Vs[[j]] %*% o$Vi %*% X %*% o$C)
  L <- cbind(0, diag(q))
  M <- L %*% o$C %*% t(L)
  e <- eigen(M, symmetric = TRUE)
  PtL <- t(e$vectors) %*% L
  Fv <- sum(drop(PtL %*% beta)^2 / e$values) / q
  nu <- sapply(seq_len(q), function(m) {
    g <- sapply(dC, function(J) drop(t(PtL[m, ]) %*% J %*% PtL[m, ]))
    2 * e$values[m]^2 / drop(t(g) %*% A %*% g)
  })
  ddf <- if (q == 1 || all(abs(nu - nu[1]) < 1e-8)) nu[1] else if (any(nu <= 2)) 2 else {
    E <- sum(nu / (nu - 2)); 2 * E / (E - q)
  }
  means <- beta[1] + c(0, beta[-1])
  list(conds = conds, F = Fv, q = q, ddf = ddf, p = pf(Fv, q, ddf, lower.tail = FALSE),
       var_subject = ph[1], var_residual = ph[2], means = means, N = N)
}

rmm_case <- function(name, subj, cond, y, classic = FALSE) {
  d <- data.frame(subj = subj, cond = cond, y = y, stringsAsFactors = FALSE)
  f <- rmm_fit(d)
  dl <- d
  dl$cond <- factor(dl$cond, levels = f$conds)
  m <- lmer(y ~ cond + (1 | subj), data = dl, REML = TRUE)
  an <- anova(m, ddf = "Satterthwaite")
  rl <- c(F = an[["F value"]], ddf = an[["DenDF"]], p = an[["Pr(>F)"]])
  for (k in names(rl)) {
    want <- c(F = f$F, ddf = f$ddf, p = f$p)[[k]]
    if (abs(rl[[k]] - want) > 1e-4 * abs(want)) stop(name, ": lmerTest ", k, " ", rl[[k]], " vs closed form ", want)
  }
  vc <- as.data.frame(VarCorr(m))
  subjects <- sort(unique(d$subj))
  complete <- sum(sapply(subjects, function(s) all(f$conds %in% d$cond[d$subj == s])))
  out <- list(
    name = name,
    subject = d$subj, condition = d$cond, value = rmm_vec(d$y),
    conditions = f$conds,
    subjects = length(subjects),
    complete_subjects = complete,
    incomplete_subjects = length(subjects) - complete,
    n_obs = f$N,
    statistic = num(f$F), df_treatment = num(f$q), df_error = num(f$ddf), p_value = num(f$p),
    var_subject = num(f$var_subject), var_residual = num(f$var_residual),
    condition_means = rmm_vec(f$means),
    r_lmertest_statistic = num(rl[["F"]]), r_lmertest_df_error = num(rl[["ddf"]]),
    r_lmertest_p_value = num(rl[["p"]]),
    r_lmertest_var_subject = num(vc$vcov[vc$grp == "subj"]),
    r_lmertest_var_residual = num(vc$vcov[vc$grp == "Residual"])
  )
  if (classic) {
    dl$subj <- factor(dl$subj)
    fit <- summary(aov(y ~ cond + Error(subj / cond), data = dl))
    within <- fit[["Error: subj:cond"]][[1]]
    out$classic_statistic <- num(within["cond", "F value"])
    out$classic_df_error <- num(within["Residuals", "Df"])
  }
  out
}

set.seed(69678)
rmm_cases <- list()
local({
  # Balanced and interior: the mixed F is the classic RM F.
  s <- sprintf("s%02d", 1:6)
  base <- round(rnorm(6, 50, 6), 2)
  eff <- c(base = 0, drug_a = 1.5, drug_b = 3.2)
  g <- expand.grid(subj = s, cond = names(eff), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + eff[g$cond] + round(rnorm(nrow(g), 0, 1.4), 2)
  rmm_cases[[1]] <<- rmm_case("balanced_six_by_three", g$subj, g$cond, y, classic = TRUE)

  # Nine subjects, four conditions, three cells missing over two subjects.
  s <- sprintf("p%02d", 1:9)
  base <- round(rnorm(9, 20, 3), 1)
  eff <- c(t1 = 0, t2 = 0.4, t3 = -0.3, t4 = 0.9)
  g <- expand.grid(subj = s, cond = names(eff), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + eff[g$cond] + round(rnorm(nrow(g), 0, 0.8), 2)
  drop <- (g$subj == "p09" & g$cond == "t3") | (g$subj == "p03" & g$cond %in% c("t1", "t4"))
  rmm_cases[[2]] <<- rmm_case("nine_by_four_three_missing", g$subj[!drop], g$cond[!drop], y[!drop])

  # Two conditions (q = 1): two subjects seen once only.
  s <- sprintf("u%02d", 1:10)
  base <- round(runif(10, 100, 140))
  g <- expand.grid(subj = s, cond = c("after", "before"), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + ifelse(g$cond == "after", -4, 0) + round(rnorm(nrow(g), 0, 3), 1)
  drop <- (g$subj == "u02" & g$cond == "after") | (g$subj == "u07" & g$cond == "before")
  rmm_cases[[3]] <<- rmm_case("two_conditions_two_singletons", g$subj[!drop], g$cond[!drop], y[!drop])

  # Five conditions, unequal subject sizes, a large subject variance.
  s <- sprintf("k%02d", 1:12)
  base <- round(rnorm(12, 0, 10), 2)
  eff <- c(a = 0, b = 1, c = 2.5, d = 0.5, e = -1)
  g <- expand.grid(subj = s, cond = names(eff), stringsAsFactors = FALSE)
  y <- base[match(g$subj, s)] + eff[g$cond] + round(rnorm(nrow(g), 0, 2), 2)
  drop <- runif(nrow(g)) < 0.2
  rmm_cases[[4]] <<- rmm_case("five_conditions_random_missing", g$subj[!drop], g$cond[!drop], y[!drop])
})

write_golden("test_anova_rm_mixed", list(
  generator = "scripts/reference/gen_test_anova_rm_mixed.R",
  r_version = R.version.string,
  packages = list(
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite")),
    lme4 = as.character(packageVersion("lme4")),
    lmerTest = as.character(packageVersion("lmerTest")),
    numDeriv = as.character(packageVersion("numDeriv"))
  ),
  primitive = "TEST_ANOVA_RM",
  r_function = "lmerTest::anova(lmer(y ~ cond + (1 | subj), REML = TRUE), ddf = \"Satterthwaite\")",
  note = "statistic / df / p and the variance components are the closed-form REML optimum and Satterthwaite df lmerTest approximates; r_lmertest_* are lmerTest's own (within 1e-4).",
  cases = rmm_cases
))
