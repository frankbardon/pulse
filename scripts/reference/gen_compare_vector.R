# gen_compare_vector.R — R oracle for a Compose nested-model comparison
# of two vector fits (`compare` over battery fits, U69 #643; owner
# decision 2026-11-24: member-wise nesting from the co-moments).
#
# Each case names a restricted battery (members R) and a full battery
# (members F ⊇ R) over the same rows. For every restricted member j, R
# fits the two Gaussian linear models
#   m0  lm(j ~ R \ {j})      (lm(j ~ 1) when R = {j})
#   m1  lm(j ~ F \ {j})
# and compares them —
#   f     anova(m0, m1)
#   lrt   anova(m0, m1, test = "Chisq")  (ΔRSS / σ̂²₁ on χ²(q))
#   wald  lmtest::waldtest(m0, m1)       (classical vcov, F = W/q)
# — every statistic re-derived from its closed form (the RSS change, and
# t(b_S) solve(V_SS) b_S for the Wald) and checked at 1e-10 relative.
# A frequency-weighted case expands each row w times (Pulse's frequency
# rule: N* = Σw, df = Σw − p), so lm on the expanded rows is the
# reference.
#
# Writes <out_dir>/compare_vector.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_compare_vector.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed.
#
# Pinned: R 4.6.1, lmtest 0.9-40 (+ zoo 1.9-1), base stats + jsonlite
# 2.0.0 — the gen_nested.R pins, nothing new. Seeded
# (set.seed(20261125)); standalone and sourced last, so no other golden
# moves. Rows are [a, b, c, d, w] (w an integer frequency weight).

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
if (R.version$major != "4" || R.version$minor != "6.1") stop("gen_compare_vector.R pins R 4.6.1, got ", R.version.string)
cv_pins <- c(lmtest = "0.9.40", zoo = "1.9.1", jsonlite = "2.0.0")
for (p in names(cv_pins)) {
  if (as.character(packageVersion(p)) != cv_pins[[p]]) stop("gen_compare_vector.R pins ", p, " ", cv_pins[[p]], ", got ", packageVersion(p))
}

cv_g17 <- function(v) {
  v <- unname(as.numeric(v))
  if (any(!is.finite(v))) stop("non-finite reference value")
  structure(paste0("[", paste(sprintf("%.17g", v), collapse = ","), "]"), class = "json")
}
cv_num <- function(x) {
  if (!is.finite(x)) stop("non-finite reference value")
  structure(sprintf("%.17g", x), class = "json")
}

cv_fit <- function(d, j, preds) {
  f <- if (length(preds) == 0) as.formula(paste(j, "~ 1")) else reformulate(preds, response = j)
  lm(f, data = d)
}

cv_cases <- list()

# cv_case runs one case over rows d (weighted: each row expanded w
# times).
cv_case <- function(name, d, restricted, full, test, weighted = FALSE) {
  fit_rows <- if (weighted) d[rep(seq_len(nrow(d)), d$w), ] else d
  tested <- setdiff(full, restricted)
  q <- length(tested)
  out <- list(name = name, test = test, weighted = weighted, n = nrow(d),
    restricted = I(restricted), full = I(full), terms = I(tested), df = q,
    distribution = if (test == "lrt") "chisq" else "f")
  members <- list()
  for (j in restricted) {
    m0 <- cv_fit(fit_rows, j, setdiff(restricted, j))
    m1 <- cv_fit(fit_rows, j, setdiff(full, j))
    rss0 <- deviance(m0)
    rss1 <- deviance(m1)
    df1 <- df.residual(m1)
    s2 <- rss1 / df1
    row <- list(member = j)
    if (test == "f") {
      a <- anova(m0, m1)
      stat <- a[2, "F"]
      p <- a[2, "Pr(>F)"]
      hand <- ((rss0 - rss1) / q) / s2
      row$dispersion <- cv_num(s2)
    } else if (test == "lrt") {
      a <- anova(m0, m1, test = "Chisq")
      p <- a[2, "Pr(>Chi)"]
      stat <- a[2, "Sum of Sq"] / s2
      hand <- (rss0 - rss1) / s2
      if (abs(p - pchisq(hand, q, lower.tail = FALSE)) > 1e-12) stop(name, ": anova lrt p differs from its closed form")
      row$dispersion <- cv_num(s2)
    } else {
      a <- waldtest(m0, m1)
      stat <- a[2, "F"]
      p <- a[2, "Pr(>F)"]
      b <- coef(m1)[tested]
      hand <- drop(t(b) %*% solve(vcov(m1)[tested, tested, drop = FALSE], b)) / q
      if (abs(hand - ((rss0 - rss1) / q) / s2) > 1e-10 * abs(hand)) stop(name, ": wald W/q is not the F statistic")
    }
    if (abs(stat - hand) > 1e-10 * abs(hand)) stop(name, ": reference ", stat, " vs closed form ", hand)
    row$statistic <- cv_num(stat)
    row$p_value <- cv_num(p)
    row$restricted_deviance <- cv_num(rss0)
    row$full_deviance <- cv_num(rss1)
    members[[length(members) + 1]] <- row
    out$restricted_df_residual <- df.residual(m0)
    out$full_df_residual <- df1
  }
  out$members <- members
  out$rows <- lapply(seq_len(nrow(d)), function(i) cv_g17(c(d$a[i], d$b[i], d$c[i], d$d[i], d$w[i])))
  cv_cases[[length(cv_cases) + 1]] <<- out
}

set.seed(20261125)
n <- 80
z <- rnorm(n)
d <- data.frame(
  a = round(z + rnorm(n, sd = 0.8), 4),
  b = round(0.6 * z + rnorm(n, sd = 1), 4),
  c = round(-0.4 * z + rnorm(n, sd = 0.9), 4),
  d = round(rnorm(n), 4),
  w = sample(1:3, n, replace = TRUE)
)

cv_case("f_two_tested", d, c("a", "b"), c("a", "b", "c", "d"), "f")
cv_case("lrt_intercept_only", d, c("a"), c("a", "c"), "lrt")
cv_case("wald_full_order", d, c("c", "b"), c("a", "b", "c"), "wald")
cv_case("f_frequency_weight", d, c("a", "c"), c("a", "b", "c", "d"), "f", weighted = TRUE)

write_golden("compare_vector", list(
  description = paste(
    "Nested-model comparison of two vector fits (U69 #643): one row per restricted battery member j,",
    "lm(j ~ other restricted members) vs lm(j ~ other full members) on the same rows:",
    "anova(m0, m1) (f), anova(m0, m1, test = 'Chisq') (lrt), lmtest::waldtest(m0, m1) (wald).",
    "A weighted case expands each row w times. Rows are [a, b, c, d, w]."),
  packages = list(R = R.version.string, lmtest = as.character(packageVersion("lmtest")),
                  zoo = as.character(packageVersion("zoo")), jsonlite = as.character(packageVersion("jsonlite"))),
  seed = 20261125,
  cases = cv_cases
))
