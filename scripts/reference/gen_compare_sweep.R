# gen_compare_sweep.R — R oracle for a Compose nested-model comparison
# reading sweep-bound fits (`compare` over `fits[].from_sweep`, U69
# #643).
#
# Each case is a sweep over the row window id <= hi (FILTER_RANGE id
# [0, hi], inclusive) whose body holds the restricted model ("small",
# y ~ restricted) and the full model ("big", y ~ full). The sweep is
# ranked by one of the full model's figures; both fits bind from the
# sweep, so both are refitted on the rank-1 slot's rows. R reproduces
# the run: it fits the full model on every candidate window, ranks the
# windows by the same figure (stopping unless the winner leads the
# runner-up by a clear margin, so no float noise can flip the winner),
# and compares the winning pair with the gen_nested.R references —
#   f     anova(m0, m1) on lm
#   lrt   anova(m0, m1, test = "LRT") on glm (poisson log)
#   wald  lmtest::waldtest(m0, m1, test = "Chisq") on glm (binomial
#         logit), classical vcov
# — every statistic re-derived from its closed form (RSS / deviance
# change, t(b_S) solve(V_SS) b_S) and checked at 1e-12 relative.
#
# Writes <out_dir>/compare_sweep.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_compare_sweep.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned: R 4.6.1, lmtest 0.9-40 (+ zoo 1.9-1), base stats + jsonlite
# 2.0.0 — the gen_nested.R pins, nothing new. glm fits use
# glm.control(epsilon = 1e-14, maxit = 200), refitted from their own
# optimum until the coefficients stop moving. Seeded (set.seed(20261124));
# standalone and sourced last, so no other golden moves. Rows are
# [id, x1, x2, x3, y].

suppressPackageStartupMessages({
  library(jsonlite)
  library(lmtest)
})

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_compare_sweep.R pins R 4.6.1, got ", R.version.string)
cs_pins <- c(lmtest = "0.9.40", zoo = "1.9.1", jsonlite = "2.0.0")
for (p in names(cs_pins)) {
  if (as.character(packageVersion(p)) != cs_pins[[p]]) stop("gen_compare_sweep.R pins ", p, " ", cs_pins[[p]], ", got ", packageVersion(p))
}

cs_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
cs_num <- function(x) {
  if (!is.finite(x)) stop("non-finite reference value")
  structure(sprintf("%.17g", x), class = "json")
}

cs_ctl <- glm.control(epsilon = 1e-14, maxit = 200)

# cs_fit fits y ~ preds on d (lm when family is NULL), polishing a glm
# from its own optimum; any warning stops the script.
cs_fit <- function(family, d, preds) {
  f <- reformulate(preds, response = "y")
  withCallingHandlers({
    if (is.null(family)) return(lm(f, data = d))
    m <- glm(f, family = family, data = d, control = cs_ctl)
    for (k in 1:20) {
      m2 <- glm(f, family = family, data = d, start = coef(m), control = cs_ctl)
      done <- max(abs(coef(m2) - coef(m))) < 1e-15
      m <- m2
      if (done) break
    }
    m
  }, warning = function(w) stop("fit warned: ", conditionMessage(w)))
}

# cs_rank_value is the full model's ranked figure on one window: the
# residual standard error (sigma) or a coefficient.
cs_rank_value <- function(m, by) {
  if (by == "residual_std_err") return(sigma(m))
  coef(m)[[sub("^coefficients\\.", "", by)]]
}

cs_cases <- list()

# cs_case runs one case: windows his (sweep order), ranked by `by` in
# `order`; the restricted / full predictor sets; the test.
cs_case <- function(name, family, link, d, his, by, order, restricted, full, test) {
  vals <- sapply(his, function(hi) cs_rank_value(cs_fit(family, d[d$id <= hi, ], full), by))
  ord <- order(if (order == "desc") -vals else vals)
  if (abs(vals[ord[1]] - vals[ord[2]]) < 1e-6 * max(1, abs(vals[ord[1]]))) stop(name, ": winner not clear of the runner-up")
  slot <- ord[1] - 1
  w <- d[d$id <= his[ord[1]], ]
  m0 <- cs_fit(family, w, restricted)
  m1 <- cs_fit(family, w, full)
  tested <- setdiff(full, restricted)
  q <- length(tested)
  out <- list(name = name, family = if (is.null(family)) "gaussian" else tolower(family$family), link = link,
    test = test, his = I(his), rank_by = by, rank_order = order, winner_slot = slot, n = nrow(w),
    restricted = I(restricted), full = I(full), terms = I(tested))
  df1 <- df.residual(m1)
  if (test == "f") {
    a <- anova(m0, m1)
    stat <- a[2, "F"]
    p <- a[2, "Pr(>F)"]
    hand <- ((deviance(m0) - deviance(m1)) / q) / (deviance(m1) / df1)
    out$distribution <- "f"
    out$df_residual <- df1
  } else if (test == "lrt") {
    a <- anova(m0, m1, test = "LRT")
    stat <- a[2, "Deviance"]
    p <- a[2, "Pr(>Chi)"]
    hand <- deviance(m0) - deviance(m1)
    out$distribution <- "chisq"
  } else {
    a <- waldtest(m0, m1, test = "Chisq")
    stat <- a[2, "Chisq"]
    p <- a[2, "Pr(>Chisq)"]
    b <- coef(m1)[tested]
    hand <- drop(t(b) %*% solve(vcov(m1)[tested, tested, drop = FALSE], b))
    out$distribution <- "chisq"
  }
  if (abs(stat - hand) > 1e-12 * abs(hand)) stop(name, ": reference ", stat, " vs closed form ", hand)
  out$rows <- lapply(seq_len(nrow(d)), function(i) cs_g17(c(d$id[i], d$x1[i], d$x2[i], d$x3[i], d$y[i])))
  out$rank_values <- cs_g17(vals)
  out$statistic <- cs_num(stat)
  out$df <- q
  out$p_value <- cs_num(p)
  out$restricted_deviance <- cs_num(deviance(m0))
  out$full_deviance <- cs_num(deviance(m1))
  out$restricted_df_residual <- df.residual(m0)
  out$full_df_residual <- df1
  cs_cases[[length(cs_cases) + 1]] <<- out
}

set.seed(20261124)
cs_design <- function(n) data.frame(id = 0:(n - 1), x1 = round(runif(n, -1, 2), 3), x2 = round(runif(n, -1, 1), 3),
                                     x3 = round(runif(n, 0, 1), 3))

d <- cs_design(90)
d$y <- 1 + 0.8 * d$x1 - 0.5 * d$x2 + 0.3 * d$x3 + rnorm(90, sd = 0.7) * (1 + 0.8 * (d$id >= 60))
cs_case("ols_f", NULL, "identity", d, c(89, 44, 59), "residual_std_err", "asc", c("x1"), c("x1", "x2", "x3"), "f")

d <- cs_design(120)
d$y <- rpois(120, exp(0.4 + 0.5 * d$x1 - 0.6 * d$x2 + 0.2 * d$x3))
cs_case("poisson_lrt", poisson("log"), "log", d, c(119, 59, 89), "coefficients.x2", "asc", c("x1"), c("x1", "x2", "x3"), "lrt")

d <- cs_design(150)
d$y <- rbinom(150, 1, plogis(-0.3 + 1.1 * d$x1 - 0.9 * d$x2 + 0.4 * d$x3))
cs_case("logit_wald", binomial("logit"), "logit", d, c(149, 99, 74), "coefficients.x2", "desc", c("x1", "x3"), c("x1", "x2", "x3"), "wald")

write_golden("compare_sweep", list(
  description = paste(
    "Nested-model comparison over sweep-bound fits (U69 #643): a sweep over the row window id <= hi,",
    "ranked by the full model's figure; R picks the winner the same way and compares the winning pair:",
    "anova(m0, m1) (lm, F), anova(m0, m1, test = 'LRT') (poisson), lmtest::waldtest(m0, m1,",
    "test = 'Chisq') (logit). Rows are [id, x1, x2, x3, y]."),
  packages = list(R = R.version.string, lmtest = as.character(packageVersion("lmtest")),
                  zoo = as.character(packageVersion("zoo")), jsonlite = as.character(packageVersion("jsonlite"))),
  seed = 20261124,
  cases = cs_cases
))
