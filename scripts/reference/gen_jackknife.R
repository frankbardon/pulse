# gen_jackknife.R — R oracle for the delete-a-group (and weighted)
# jackknife of REG_OLS / REG_GLM (model-workbench E3-S2, #343).
#
# Every case is computed by MANUAL DELETIONS: for each delete group k the
# model is refitted without group k's rows (lm / glm / glmnet on the
# remaining rows, with their own weights), and
#
#   SE[j]   = sqrt((g - 1)/g * sum_k (b_(k)[j] - bbar[j])^2)
#   V[i,j]  = (g - 1)/g * sum_k (b_(k)[i] - bbar[i]) (b_(k)[j] - bbar[j])
#
# bbar the mean of the g replicates. A leave-one-out case is the same
# formula with every row its own group. The unweighted, unpenalised
# cluster cases are cross-checked against survey's JK1 replicate design
# (as.svrepdesign(type = "JK1", mse = FALSE) + svyglm) at relative 1e-8
# before anything is written, so the fixture pins the textbook JK1
# variance, not a hand-rolled convention.
#
# Writes <out_dir>/jackknife.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable
# on its own too:
#
#   Rscript scripts/reference/gen_jackknife.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned: R 4.6.1, survey 4.5, glmnet 5.1, jsonlite (the versions the
# fixture's `packages` block records). glm fits use
# glm.control(epsilon = 1e-14, maxit = 200); glmnet
# control = list(thresh = 1e-20, maxit = 1e7) as in gen_glm_penalty.R.
#
# rows are [x1, x2, y, w, c]: c is the delete-group id (the jackknife_by
# field; a grouped case's c is the assignment the Go test hands
# jackknife_by — the random assignment itself is Go's, pinned by a
# separate same-process test). w is a frequency weight (1 when the case
# is unweighted).

suppressPackageStartupMessages({
  library(jsonlite)
  library(survey)
  library(glmnet)
})

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE, null = "null")
    cat(txt, file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}

jk_num <- function(x) {
  if (length(x) != 1) stop("jk_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
jk_vec <- function(v) lapply(unname(as.numeric(v)), jk_num)

# jk_fit fits one model on data frame d (columns x1, x2, y, w) and
# returns (intercept, x1, x2).
jk_fit <- function(d, type, family, penalty, alpha) {
  if (type == "REG_OLS") {
    return(unname(coef(lm(y ~ x1 + x2, data = d, weights = w))))
  }
  if (penalty == "") {
    fit <- suppressWarnings(glm(y ~ x1 + x2, data = d, weights = w, family = family,
                                control = glm.control(epsilon = 1e-14, maxit = 200)))
    if (!fit$converged) stop("glm did not converge")
    # A separated replicate has no MLE (Pulse refuses it,
    # PROCESSING_REGRESSION_SEPARATION): the fixture must not hold one.
    if (any(fitted(fit) < 1e-9 | fitted(fit) > 1 - 1e-9)) stop("a glm replicate is separated")
    return(unname(coef(fit)))
  }
  # l2 on glmnet's objective (Pulse alpha = glmnet lambda, l1_ratio 0).
  fit <- glmnet(as.matrix(d[, c("x1", "x2")]), d$y, family = family, weights = d$w,
                alpha = 0, lambda = alpha, standardize = FALSE, intercept = TRUE,
                control = list(thresh = 1e-20, maxit = 1e7))
  as.numeric(as.matrix(coef(fit)))
}

jk_cases <- list()

jk_case <- function(name, type, family, d, penalty = "", alpha = 0, loo = FALSE, check_survey = FALSE) {
  full <- jk_fit(d, type, family, penalty, alpha)
  groups <- if (loo) seq_len(nrow(d)) else sort(unique(d$c))
  reps <- t(sapply(groups, function(k) {
    keep <- if (loo) seq_len(nrow(d)) != k else d$c != k
    jk_fit(d[keep, ], type, family, penalty, alpha)
  }))
  g <- nrow(reps)
  bbar <- colMeans(reps)
  centred <- sweep(reps, 2, bbar)
  V <- (g - 1) / g * crossprod(centred)
  se <- sqrt(diag(V))
  if (check_survey) {
    des <- svydesign(ids = ~c, data = d, weights = ~w)
    rep <- as.svrepdesign(des, type = "JK1", mse = FALSE)
    sfit <- if (type == "REG_OLS") {
      svyglm(y ~ x1 + x2, design = rep)
    } else {
      suppressWarnings(svyglm(y ~ x1 + x2, design = rep, family = quasibinomial(),
                              control = glm.control(epsilon = 1e-14, maxit = 200)))
    }
    sv <- unname(as.matrix(vcov(sfit)))
    if (max(abs(sv - V)) > 1e-8 * max(abs(V))) {
      stop(name, ": manual deletions disagree with survey JK1: ", max(abs(sv - V)))
    }
  }
  rows <- lapply(seq_len(nrow(d)), function(i) jk_vec(c(d$x1[i], d$x2[i], d$y[i], d$w[i], d$c[i])))
  jk_cases[[length(jk_cases) + 1]] <<- list(
    name = name, type = type, family = if (type == "REG_GLM") family else "",
    penalty = penalty, alpha = jk_num(alpha), leave_one_out = loo,
    weighted = any(d$w != 1), groups = g,
    columns = I(c("x1", "x2", "y", "w", "c")), rows = rows,
    coefficients = jk_vec(full), std_errors = jk_vec(se),
    vcov = lapply(seq_len(nrow(V)), function(i) jk_vec(V[i, ])))
}

set.seed(343)
n <- 90
x1 <- round(rnorm(n), 4)
x2 <- round(runif(n, -1, 1), 4)
# Unequal cluster sizes: 9 clusters.
cl <- rep(1:9, times = c(6, 8, 10, 12, 9, 11, 7, 13, 14))
yo <- round(1.5 + 0.8 * x1 - 0.6 * x2 + rnorm(n, sd = 0.7) + 0.3 * (cl %% 3), 4)
yb <- as.numeric(runif(n) < 1 / (1 + exp(-(-0.2 + 0.6 * x1 - 0.5 * x2))))
fw <- sample(1:3, n, replace = TRUE)
ones <- rep(1, n)
# A grouped case's c: a balanced 6-group assignment (the Go side's
# jackknife_groups permutation is pinned separately).
gr <- (sample.int(n) - 1) %% 6 + 1

jk_case("ols_cluster", "REG_OLS", "", data.frame(x1, x2, y = yo, w = ones, c = cl), check_survey = TRUE)
jk_case("glm_cluster", "REG_GLM", "binomial", data.frame(x1, x2, y = yb, w = ones, c = cl), check_survey = TRUE)
jk_case("ols_grouped", "REG_OLS", "", data.frame(x1, x2, y = yo, w = ones, c = gr))
jk_case("glm_grouped", "REG_GLM", "binomial", data.frame(x1, x2, y = yb, w = ones, c = gr))
jk_case("ols_cluster_weighted", "REG_OLS", "", data.frame(x1, x2, y = yo, w = fw, c = cl))
jk_case("glm_grouped_weighted", "REG_GLM", "binomial", data.frame(x1, x2, y = yb, w = fw, c = gr))
jk_case("glm_l2_cluster", "REG_GLM", "binomial", data.frame(x1, x2, y = yb, w = ones, c = cl),
        penalty = "l2", alpha = 0.05)
# Weighted leave-one-out on the first 60 rows.
k <- 1:60
jk_case("ols_loo_weighted", "REG_OLS", "", data.frame(x1 = x1[k], x2 = x2[k], y = yo[k], w = fw[k], c = k), loo = TRUE)
jk_case("glm_loo_weighted", "REG_GLM", "binomial", data.frame(x1 = x1[k], x2 = x2[k], y = yb[k], w = fw[k], c = k), loo = TRUE)

jk_doc <- list(
  generator = "scripts/reference/gen_jackknife.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(survey = as.character(packageVersion("survey")),
                  glmnet = as.character(packageVersion("glmnet")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "REG_OLS / REG_GLM jackknife: delete-a-group (jackknife_by / jackknife_groups), weighted (JK1) and leave-one-out",
  r_function = "manual deletions: lm / glm(control = glm.control(epsilon = 1e-14)) / glmnet(alpha = 0, standardize = FALSE) on the rows outside each delete group",
  note = paste(
    "rows are [x1, x2, y, w, c]; c is the delete-group id, w a frequency weight.",
    "SE = sqrt((g - 1)/g * sum_k (b_(k) - bbar)^2); vcov the same scaled cross-product.",
    "Unweighted unpenalised cluster cases are cross-checked against survey JK1 (mse = FALSE) at relative 1e-8.",
    "leave_one_out cases make every row its own group."),
  cases = jk_cases)
write_golden("jackknife", jk_doc)
