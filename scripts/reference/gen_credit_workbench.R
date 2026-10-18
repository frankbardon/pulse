# gen_credit_workbench.R — R oracle for the model-workbench end-to-end
# acceptance (U49 E3-S4): a credit-shaped loan book, a train / holdout
# split by vintage, a fit_on target encoding, a ridge-penalised binomial
# GLM with delete-a-vintage jackknife standard errors, holdout scoring,
# the holdout AUC with its DeLong interval, and the holdout decile
# (gains) table.
#
# Writes two files under <out_dir>:
#
#   credit/loans.csv       the committed fixture the Go test imports
#                          (fixed-decimal text, so the CSV holds exactly
#                          the doubles R used);
#   credit_workbench.json  every figure the pipeline must reproduce,
#                          plus the fixture's sha256 (the Go test refuses
#                          a fixture that drifted from its oracle).
#
# Sourced at the end of gen_reference.R (`make reference`, which appends
# the `// golden-hash:` footer to the JSON); runnable on its own too:
#
#   Rscript scripts/reference/gen_credit_workbench.R internal/processing/testdata/reference
#
# (then append the footer by hand as the Makefile's reference target
# does). CI never runs R: both files are committed.
#
# Pinned: R 4.6.1, glmnet 5.1, pROC 1.19.1 (refused otherwise),
# jsonlite; the fixture sha256 is taken with `shasum -a 256`.
#
# The pipeline, step by step, as the Go test runs it through Pulse:
#
#  1. Train = vintage <= 18, holdout = vintage >= 19.
#  2. te_purpose = FEAT_TARGET_ENCODE(purpose, target = defaulted,
#     smoothing = 5, fit_on = "vintage <= 18"): over the TRAINING rows,
#     (sum_cat + s * global) / (n_cat + s), global the training default
#     rate; every row (holdout included) encoded from those means.
#  3. pd = REG_GLM binomial, defaulted ~ te_purpose + income + dti +
#     collections_flag on the training rows, penalty l2, alpha = 0.002:
#     glmnet(alpha = 0, lambda = 0.002, standardize = FALSE,
#     thresh = 1e-20) — Pulse alpha is glmnet lambda on the same
#     objective. resample jackknife, jackknife_by vintage: 18 delete-a-
#     vintage replicates refit by glmnet on the remaining training rows
#     (te_purpose held at step 2's values: features run once, before the
#     fit), SE = sqrt((g-1)/g * sum_k (b_k - bbar)^2).
#  4. score = the fit's response-scale prediction on every row.
#  5. AUC of score for defaulted: pROC roc(levels = c(0, 1),
#     direction = "<"), auc, var(method = "delong"), ci.auc(delong) — on
#     the holdout and on the training rows.
#  6. Holdout deciles: GROUP_QUANTILE interval 10 — sorted ascending by
#     score, the row at 0-based rank r in bucket floor(r * 10 / n) (D1 the
#     lowest scores). Per decile: loans, defaults, mean score; default
#     share = defaults / total defaults; cumulative share and cumulative
#     lift ordered by mean score DESCENDING (riskiest first): running
#     defaults share / running loans share; lift = default share / loan
#     share.
#
# chargeoff_amt is a post-outcome leak (> 0 exactly when defaulted): a
# GLM on it is completely separated, which Pulse refuses
# (PROCESSING_REGRESSION_SEPARATION). The oracle asserts the separation
# holds on the training rows.

cw_pinned <- c(glmnet = "5.1", pROC = "1.19.1")
for (p in names(cw_pinned)) {
  have <- tryCatch(as.character(packageVersion(p)), error = function(e) "missing")
  if (have != cw_pinned[[p]]) {
    stop(sprintf("package %s is %s, the oracle is pinned to %s (see the header)", p, have, cw_pinned[[p]]))
  }
}
suppressPackageStartupMessages({
  library(jsonlite)
  library(glmnet)
  library(pROC)
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
    message("wrote ", path)
  }
}

cw_num <- function(x) {
  if (length(x) != 1) stop("cw_num() takes a scalar")
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
cw_vec <- function(v) lapply(unname(as.numeric(v)), cw_num)

# ---- the loan book --------------------------------------------------
set.seed(49)
n_vintage <- 24
per_vintage <- 125
n <- n_vintage * per_vintage
purposes <- c("auto", "business", "debt_consolidation", "education", "home", "medical")
purpose_eff <- c(auto = -0.5, business = 0.6, debt_consolidation = 0.2, education = 0.1, home = -0.8, medical = 0.4)

vintage <- rep(seq_len(n_vintage), each = per_vintage)
purpose <- sample(purposes, n, replace = TRUE, prob = c(0.2, 0.1, 0.3, 0.1, 0.2, 0.1))
income <- round(exp(rnorm(n, log(60), 0.45)), 1)
dti <- round(runif(n, 2, 45), 1)
collections_flag <- as.integer(runif(n) < 0.08)
eta <- -2.0 + purpose_eff[purpose] - 0.015 * (income - 60) + 0.035 * (dti - 20) + 1.7 * collections_flag
defaulted <- as.integer(runif(n) < plogis(eta))
chargeoff_amt <- ifelse(defaulted == 1, round(runif(n, 500, 25000)), 0)

loans <- data.frame(id = seq_len(n), vintage, purpose, income, dti, collections_flag, defaulted, chargeoff_amt,
                    stringsAsFactors = FALSE)

fixture_dir <- file.path(out_dir, "credit")
dir.create(fixture_dir, recursive = TRUE, showWarnings = FALSE)
fixture_path <- file.path(fixture_dir, "loans.csv")
lines <- c("id,vintage,purpose,income,dti,collections_flag,defaulted,chargeoff_amt",
           sprintf("%d,%d,%s,%.1f,%.1f,%d,%d,%d", loans$id, loans$vintage, loans$purpose, loans$income, loans$dti,
                   loans$collections_flag, loans$defaulted, as.integer(loans$chargeoff_amt)))
writeLines(lines, fixture_path)
# Read back: the oracle computes on exactly the doubles the CSV holds.
loans <- read.csv(fixture_path, stringsAsFactors = FALSE)
fixture_sha <- sub(" .*", "", system2("shasum", c("-a", "256", shQuote(fixture_path)), stdout = TRUE))

train <- loans$vintage <= 18
hold <- !train
if (!all((loans$chargeoff_amt[train] > 0) == (loans$defaulted[train] == 1))) stop("chargeoff_amt does not separate")

# ---- 2. fit_on target encoding --------------------------------------
smoothing <- 5
g_mean <- mean(loans$defaulted[train])
cat_sum <- tapply(loans$defaulted[train], loans$purpose[train], sum)
cat_n <- tapply(loans$defaulted[train], loans$purpose[train], length)
enc <- (cat_sum + smoothing * g_mean) / (cat_n + smoothing)
loans$te_purpose <- unname(enc[loans$purpose])
if (anyNA(loans$te_purpose)) stop("a purpose has no training rows")

# ---- 3. ridge GLM + delete-a-vintage jackknife -----------------------
alpha <- 0.002
predictors <- c("te_purpose", "income", "dti", "collections_flag")
cw_fit <- function(d) {
  fit <- glmnet(as.matrix(d[, predictors]), d$defaulted, family = "binomial", alpha = 0, lambda = alpha,
                standardize = FALSE, intercept = TRUE, control = list(thresh = 1e-20, maxit = 1e7))
  as.numeric(as.matrix(coef(fit)))
}
tr <- loans[train, ]
beta <- cw_fit(tr)
vintages <- sort(unique(tr$vintage))
reps <- t(sapply(vintages, function(k) cw_fit(tr[tr$vintage != k, ])))
g <- nrow(reps)
bbar <- colMeans(reps)
se <- sqrt((g - 1) / g * colSums(sweep(reps, 2, bbar)^2))

# ---- 4. scores ------------------------------------------------------
X <- cbind(1, as.matrix(loans[, predictors]))
loans$score <- as.numeric(plogis(X %*% beta))

# ---- 5. AUC + DeLong ------------------------------------------------
cw_auc <- function(rows) {
  r <- roc(response = loans$defaulted[rows], predictor = loans$score[rows], levels = c(0, 1), direction = "<", quiet = TRUE)
  ci <- ci.auc(r, method = "delong", conf.level = 0.95)
  list(n = sum(rows), n_pos = sum(loans$defaulted[rows] == 1), n_neg = sum(loans$defaulted[rows] == 0),
       auc = cw_num(as.numeric(auc(r))), se = cw_num(sqrt(var(r, method = "delong"))),
       ci_lower = cw_num(ci[1]), ci_upper = cw_num(ci[3]))
}

# ---- 6. holdout deciles ---------------------------------------------
h <- loans[hold, ]
m <- nrow(h)
ord <- order(h$score)
bucket <- integer(m)
bucket[ord] <- ((seq_len(m) - 1) * 10) %/% m
deciles <- lapply(0:9, function(b) {
  rows <- bucket == b
  list(key = paste0("D", b + 1), loans = sum(rows), defaults = sum(h$defaulted[rows]),
       mean_pd = mean(h$score[rows]))
})
tot_def <- sum(h$defaulted)
mean_pd <- sapply(deciles, function(d) d$mean_pd)
loans_k <- sapply(deciles, function(d) d$loans)
defs_k <- sapply(deciles, function(d) d$defaults)
risk <- order(mean_pd, decreasing = TRUE)
cum_def <- numeric(10)
cum_loans <- numeric(10)
cum_def[risk] <- cumsum(defs_k[risk])
cum_loans[risk] <- cumsum(loans_k[risk])
for (k in 1:10) {
  deciles[[k]]$mean_pd <- cw_num(deciles[[k]]$mean_pd)
  deciles[[k]]$default_share <- cw_num(defs_k[k] / tot_def)
  deciles[[k]]$lift <- cw_num((defs_k[k] / tot_def) / (loans_k[k] / m))
  deciles[[k]]$cum_default_share <- cw_num(cum_def[k] / tot_def)
  deciles[[k]]$cum_lift <- cw_num((cum_def[k] / tot_def) / (cum_loans[k] / m))
}

cw_doc <- list(
  generator = "scripts/reference/gen_credit_workbench.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(glmnet = as.character(packageVersion("glmnet")), pROC = as.character(packageVersion("pROC")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "model-workbench end to end: fit_on target encoding, ridge REG_GLM, delete-a-vintage jackknife, ATTR_REG_PREDICT, AGG_AUC (DeLong), GROUP_QUANTILE deciles with WIN_SHARE / WIN_LIFT",
  r_function = "glmnet(alpha = 0, standardize = FALSE, thresh = 1e-20) per fit and per replicate; pROC roc / var(delong) / ci.auc(delong)",
  note = "fixture credit/loans.csv (sha256 below); train vintage <= 18, holdout >= 19; coefficients ordered (intercept), te_purpose, income, dti, collections_flag",
  fixture = "credit/loans.csv",
  fixture_sha256 = fixture_sha,
  rows = n,
  train_vintage_max = 18,
  smoothing = cw_num(smoothing),
  alpha = cw_num(alpha),
  predictors = I(predictors),
  te_purpose = lapply(as.list(enc), cw_num),
  coefficients = cw_vec(beta),
  jackknife_groups = g,
  std_errors = cw_vec(se),
  holdout_auc = cw_auc(hold),
  train_auc = cw_auc(train),
  deciles = deciles)
write_golden("credit_workbench", cw_doc)
