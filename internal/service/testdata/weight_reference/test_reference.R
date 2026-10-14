# Frequency-weight reference for the weighted significance tests
# (weighting-inferential E1-S4, .claude/reference/weighting.md
# "Weighted inference"). Called by gen_weight_reference.py — never by CI.
#
# Base R only (stats): every figure is the STOCK R function run on the
# rep()-expanded rows, the definition of a frequency weight. Pinned:
# R 4.6.1 (refuses any other version).
#
# Usage: Rscript test_reference.R <csv> <mu>
#   csv columns x,y,h,k,o,f — x may be NA; f a non-negative integer
#   frequency weight (rows with an invalid weight are already dropped).
# Prints one line "R <ver>", then "<case> <figure> <value>" lines at 17
# significant digits.
want <- "4.6.1"
have <- sprintf("%s.%s", R.version$major, R.version$minor)
if (have != want) stop(sprintf("R %s required, found %s", want, have))
args <- commandArgs(trailingOnly = TRUE)
if (length(args) != 2) stop("usage: test_reference.R <csv> <mu>")
d <- read.csv(args[[1]], stringsAsFactors = FALSE)
mu <- as.numeric(args[[2]])
e <- d[rep(seq_len(nrow(d)), d$f), ]
e$h <- factor(e$h, levels = sort(unique(e$h)))
e$k <- factor(e$k, levels = sort(unique(e$k)))

cat(sprintf("R %s\n", have))
out <- function(case, fig, v) cat(sprintf("%s %s %.17g\n", case, fig, v))

# TEST_T one-sample.
tt <- t.test(e$x, mu = mu)
out("t_one", "statistic", tt$statistic)
out("t_one", "df", tt$parameter)
out("t_one", "p_value", tt$p.value)
out("t_one", "mean", tt$estimate)
out("t_one", "variance", var(e$x, na.rm = TRUE))
out("t_one", "ci_low", tt$conf.int[1])
out("t_one", "ci_high", tt$conf.int[2])

# TEST_T split / TEST_WELCH: Welch two-sample (var.equal = FALSE), a − b.
xs <- e[!is.na(e$x), ]
tw <- t.test(x ~ h, data = xs)
out("welch", "statistic", tw$statistic)
out("welch", "df", tw$parameter)
out("welch", "p_value", tw$p.value)
out("welch", "mean_a", tw$estimate[1])
out("welch", "mean_b", tw$estimate[2])
out("welch", "ci_low", tw$conf.int[1])
out("welch", "ci_high", tw$conf.int[2])
va <- var(xs$x[xs$h == "a"])
vb <- var(xs$x[xs$h == "b"])
out("welch", "variance_a", va)
out("welch", "variance_b", vb)

# TEST_Z_TWO_SAMPLE: the Welch standard error, normal tail.
na <- sum(xs$h == "a")
nb <- sum(xs$h == "b")
z <- (tw$estimate[1] - tw$estimate[2]) / sqrt(va / na + vb / nb)
out("z", "statistic", z)
out("z", "p_value", 2 * pnorm(-abs(z)))
# Wald interval on the difference, normal critical qnorm(0.975)
# (weighting-inferential E5-S1).
zh <- qnorm(0.975) * sqrt(va / na + vb / nb)
out("z", "ci_low", tw$estimate[1] - tw$estimate[2] - zh)
out("z", "ci_high", tw$estimate[1] - tw$estimate[2] + zh)

# TEST_PAIRED_T on x − y (pairs with a missing side dropped).
tp <- t.test(e$x, e$y, paired = TRUE)
out("paired", "statistic", tp$statistic)
out("paired", "df", tp$parameter)
out("paired", "p_value", tp$p.value)
out("paired", "mean_diff", tp$estimate)
out("paired", "ci_low", tp$conf.int[1])
out("paired", "ci_high", tp$conf.int[2])

# TEST_ANOVA_F: one-way, equal variances; sums of squares from aov.
of <- oneway.test(x ~ k, data = xs, var.equal = TRUE)
out("anova_f", "statistic", of$statistic)
out("anova_f", "df_between", of$parameter[1])
out("anova_f", "df_within", of$parameter[2])
out("anova_f", "p_value", of$p.value)
ss <- summary(aov(x ~ k, data = xs))[[1]][["Sum Sq"]]
out("anova_f", "ss_between", ss[1])
out("anova_f", "ss_within", ss[2])
gm <- tapply(xs$x, xs$k, mean)
for (g in names(gm)) out("anova_f", paste0("mean_", g), gm[[g]])

# TEST_ANOVA_WELCH.
ow <- oneway.test(x ~ k, data = xs, var.equal = FALSE)
out("anova_welch", "statistic", ow$statistic)
out("anova_welch", "df_between", ow$parameter[1])
out("anova_welch", "df_within", ow$parameter[2])
out("anova_welch", "p_value", ow$p.value)

# TEST_PEARSON_R.
ct <- cor.test(e$x, e$y)
out("pearson", "r", ct$estimate)
out("pearson", "t", ct$statistic)
out("pearson", "df", ct$parameter)
out("pearson", "p_value", ct$p.value)
out("pearson", "ci_low", ct$conf.int[1])
out("pearson", "ci_high", ct$conf.int[2])

# TEST_CHISQ: h × o, no continuity correction.
tab <- table(e$h, e$o)
cs <- suppressWarnings(chisq.test(tab, correct = FALSE))
out("chisq", "statistic", cs$statistic)
out("chisq", "df", cs$parameter)
out("chisq", "p_value", cs$p.value)
out("chisq", "expected_min", min(cs$expected))
out("chisq", "cramers_v", sqrt(cs$statistic / (sum(tab) * (min(dim(tab)) - 1))))

# TEST_PROP_Z: success "yes" by h; z = sign(p_a − p_b)·√X².
succ <- c(sum(e$o[e$h == "a"] == "yes"), sum(e$o[e$h == "b"] == "yes"))
tot <- c(sum(e$h == "a"), sum(e$h == "b"))
pt <- prop.test(succ, tot, correct = FALSE)
out("prop_z", "statistic", sign(pt$estimate[1] - pt$estimate[2]) * sqrt(pt$statistic))
out("prop_z", "p_value", pt$p.value)
out("prop_z", "proportion_a", pt$estimate[1])
out("prop_z", "proportion_b", pt$estimate[2])
out("prop_z", "ci_low", pt$conf.int[1])
out("prop_z", "ci_high", pt$conf.int[2])
out("prop_z", "pooled", sum(succ) / sum(tot))

# Rank tests (weighting-inferential E2-S1; frequency-only): the
# asymptotic forms Pulse computes, each on the expanded rows.
# TEST_MANN_WHITNEY_U: x by h, a vs b; continuity-corrected normal.
mw <- wilcox.test(x ~ h, data = xs, exact = FALSE, correct = TRUE)
nma <- sum(xs$h == "a")
nmb <- sum(xs$h == "b")
out("mann_whitney", "u_a", mw$statistic)
out("mann_whitney", "u_min", min(mw$statistic, nma * nmb - mw$statistic))
out("mann_whitney", "p_value", mw$p.value)
out("mann_whitney", "rank_biserial", 2 * mw$statistic / (nma * nmb) - 1)

# TEST_WILCOXON_SR on x − y (pairs with a missing side and zero
# differences dropped); continuity-corrected normal.
pr <- e[!is.na(e$x), ]
ws <- suppressWarnings(wilcox.test(pr$x, pr$y, paired = TRUE, exact = FALSE, correct = TRUE))
dz <- pr$x - pr$y
nz <- sum(dz != 0)
vp <- ws$statistic
vm <- nz * (nz + 1) / 2 - vp
out("wilcoxon_sr", "w_plus", vp)
out("wilcoxon_sr", "w_minus", vm)
out("wilcoxon_sr", "statistic", min(vp, vm))
out("wilcoxon_sr", "p_value", ws$p.value)
out("wilcoxon_sr", "rank_biserial", (vp - vm) / (vp + vm))

# TEST_KRUSKAL_WALLIS: x by k, tie-corrected H.
kw <- kruskal.test(x ~ k, data = xs)
out("kruskal", "statistic", kw$statistic)
out("kruskal", "df", kw$parameter)
out("kruskal", "p_value", kw$p.value)
rs <- tapply(rank(xs$x), xs$k, sum)
for (g in names(rs)) out("kruskal", paste0("rank_sum_", g), rs[[g]])

# TEST_SPEARMAN_R: rho and cor.test's default p (exact = NULL: the
# expansion is tied, so the t_{n-2} approximation).
sp <- suppressWarnings(cor.test(pr$x, pr$y, method = "spearman"))
nsp <- nrow(pr)
out("spearman", "rho", sp$estimate)
out("spearman", "p_value", sp$p.value)
out("spearman", "t", sp$estimate * sqrt((nsp - 2) / (1 - sp$estimate^2)))
out("spearman", "df", nsp - 2)

# TEST_KENDALL_TAU: tau-b and cor.test's default p (exact = NULL,
# continuity = FALSE: the expansion is tied, so the tie-adjusted normal
# approximation, z = S / sqrt(Var(S))).
kt <- suppressWarnings(cor.test(pr$x, pr$y, method = "kendall"))
out("kendall", "tau", kt$estimate)
out("kendall", "z", kt$statistic)
out("kendall", "p_value", kt$p.value)

# Exact / distribution / spread tests (weighting-inferential E2-S2;
# frequency-only), each on the expanded rows.
# TEST_FISHER_EXACT: h × o with o = "maybe" filtered out; the two-sided
# p (R's estimate is the conditional MLE, not Pulse's sample odds ratio).
fe <- e[e$o != "maybe", ]
ft <- fisher.test(table(fe$h, factor(fe$o)))
out("fisher", "p_value", ft$p.value)

# TEST_KS: x by h; D only (Pulse's p is the Stephens-corrected
# asymptotic, not ks.test's).
ks <- suppressWarnings(ks.test(xs$x[xs$h == "a"], xs$x[xs$h == "b"], exact = FALSE))
out("ks", "statistic", ks$statistic)

# TEST_BROWN_FORSYTHE: x by k — car::leveneTest(center = median)'s own
# computation, anova(lm(|x − group median| ~ group)), in base R (no car
# dependency to pin).
md <- ave(xs$x, xs$k, FUN = median)
bf <- anova(lm(abs(xs$x - md) ~ xs$k))
out("brown_forsythe", "statistic", bf[["F value"]][1])
out("brown_forsythe", "df_between", bf[["Df"]][1])
out("brown_forsythe", "df_within", bf[["Df"]][2])
out("brown_forsythe", "p_value", bf[["Pr(>F)"]][1])
out("brown_forsythe", "ss_between", bf[["Sum Sq"]][1])
out("brown_forsythe", "ss_within", bf[["Sum Sq"]][2])
gmd <- tapply(xs$x, xs$k, median)
for (g in names(gmd)) out("brown_forsythe", paste0("median_", g), gmd[[g]])

# TEST_HOTELLING_T2 on the (x, y) mean vector (U25 E1-S1), stats::anova.mlm
# Hotelling-Lawley — exact for an intercept-only model (one-sample, on
# x - mu) and for a two-level factor (two-sample, pooled covariance):
# T2 = HL * (n - 1) one-sample, HL * (n - 2) two-sample; F and p as R
# reports them.
Y1 <- cbind(xs$x - mu, xs$y - mu)
a1 <- anova(lm(Y1 ~ 1), test = "Hotelling-Lawley")
out("hotelling_one", "t2", a1[["Hotelling-Lawley"]][1] * (nrow(Y1) - 1))
out("hotelling_one", "f", a1[["approx F"]][1])
out("hotelling_one", "df2", a1[["den Df"]][1])
out("hotelling_one", "p_value", a1[["Pr(>F)"]][1])
Y2 <- cbind(xs$x, xs$y)
a2 <- anova(lm(Y2 ~ h, data = xs), test = "Hotelling-Lawley")
out("hotelling_split", "t2", a2[["Hotelling-Lawley"]][2] * (nrow(Y2) - 2))
out("hotelling_split", "f", a2[["approx F"]][2])
out("hotelling_split", "df2", a2[["den Df"]][2])
out("hotelling_split", "p_value", a2[["Pr(>F)"]][2])

# TEST_MANOVA on the (x, y) mean vector across k (U25 E1-S3):
# summary(manova(...)) for each of the four statistics; figure names are
# "<statistic>_<column>".
fm <- manova(cbind(x, y) ~ k, data = xs)
for (tn in c(pillai = "Pillai", wilks = "Wilks", hotelling_lawley = "Hotelling-Lawley", roy = "Roy")) {
  key <- names(which(c(pillai = "Pillai", wilks = "Wilks", hotelling_lawley = "Hotelling-Lawley", roy = "Roy") == tn))
  st <- summary(fm, test = tn)$stats[1, ]
  out("manova", paste0(key, "_statistic"), st[[2]])
  out("manova", paste0(key, "_f"), st[[3]])
  out("manova", paste0(key, "_df1"), st[[4]])
  out("manova", paste0(key, "_df2"), st[[5]])
  out("manova", paste0(key, "_p_value"), st[[6]])
}

# TEST_BARTLETT_SPHERICITY on (x, y) (U25 E1-S4): psych::cortest.bartlett's
# closed form in base R, -log(det(R)) * (n - 1 - (2p + 5)/6).
xy <- as.matrix(xs[, c("x", "y")])
bR <- cor(xy); bn <- nrow(xy); bp <- ncol(xy)
bchi <- -log(det(bR)) * (bn - 1 - (2 * bp + 5) / 6)
out("bartlett_sphericity", "statistic", bchi)
out("bartlett_sphericity", "df", bp * (bp - 1) / 2)
out("bartlett_sphericity", "p_value", pchisq(bchi, bp * (bp - 1) / 2, lower.tail = FALSE))
out("bartlett_sphericity", "determinant", det(bR))

# TEST_BOX_M on (x, y) across k (U25 E1-S4): biotools::boxM's chi-square
# in base R, plus Box's F approximation (SPSS's form).
bl <- levels(xs$k)
bnu <- sapply(bl, function(l) sum(xs$k == l) - 1)
bS <- lapply(bl, function(l) cov(xy[xs$k == l, , drop = FALSE]))
bSp <- Reduce(`+`, Map(`*`, bS, bnu)) / sum(bnu)
bM <- sum(bnu) * log(det(bSp)) - sum(bnu * sapply(bS, function(s) log(det(s))))
bk <- length(bl)
bA1 <- (2 * bp^2 + 3 * bp - 1) / (6 * (bp + 1) * (bk - 1)) * (sum(1 / bnu) - 1 / sum(bnu))
bA2 <- (bp - 1) * (bp + 2) / (6 * (bk - 1)) * (sum(1 / bnu^2) - 1 / sum(bnu)^2)
bdf1 <- bp * (bp + 1) / 2 * (bk - 1)
if (bA2 - bA1^2 > 0) {
  bdf2 <- (bdf1 + 2) / (bA2 - bA1^2); bF <- bM / (bdf1 / (1 - bA1 - bdf1 / bdf2))
} else {
  bdf2 <- (bdf1 + 2) / (bA1^2 - bA2); bb <- bdf2 / (1 - bA1 + 2 / bdf2); bF <- bdf2 * bM / (bdf1 * (bb - bM))
}
out("box_m", "statistic", bM * (1 - bA1))
out("box_m", "df", bdf1)
out("box_m", "p_value", pchisq(bM * (1 - bA1), bdf1, lower.tail = FALSE))
out("box_m", "m", bM)
out("box_m", "f", bF)
out("box_m", "df2", bdf2)
out("box_m", "f_p_value", pf(bF, bdf1, bdf2, lower.tail = FALSE))

# TEST_MARDIA on (x, y) (U25 E1-S5): MVN::mvn(mvnTest = "mardia")'s
# closed form in base R (cov = TRUE: the ML covariance).
mn <- nrow(xy); mp <- ncol(xy)
mc <- sweep(xy, 2, colMeans(xy))
mD <- mc %*% solve(crossprod(mc) / mn) %*% t(mc)
mb1 <- sum(mD^3) / mn^2; mb2 <- sum(diag(mD)^2) / mn
mdf <- mp * (mp + 1) * (mp + 2) / 6
mskew <- mn * mb1 / 6
if (mn < 20) mskew <- mskew * (mp + 1) * (mn + 1) * (mn + 3) / (mn * ((mn + 1) * (mp + 1) - 6))
mz <- (mb2 - mp * (mp + 2)) * sqrt(mn / (8 * mp * (mp + 2)))
out("mardia", "statistic", mskew)
out("mardia", "df", mdf)
out("mardia", "p_value", pchisq(mskew, mdf, lower.tail = FALSE))
out("mardia", "skewness", mb1)
out("mardia", "kurtosis", mb2)
out("mardia", "kurtosis_z", mz)
out("mardia", "kurtosis_p_value", 2 * pnorm(-abs(mz)))

# Inferential overlays (weighting-inferential E3-S3), each on the
# expanded rows; figure names ARE the Go figure keys
# ("<layer>/<wire path>", see weight_reference_overlays_test.go). The
# Compose slots: total = every row, sub = y > 4, sub2 = y < 6.
lh <- c("a", "b")
lk <- c("p", "q", "r")
lo <- c("maybe", "no", "yes")
tab_of <- function(d) table(factor(d$h, levels = lh), factor(d$o, levels = lo))
gof <- function(obs, p) suppressWarnings(chisq.test(obs, p = p))
pz <- function(s, n) suppressWarnings(prop.test(s, n, correct = FALSE))$p.value

# OVERLAY_CHISQ_MATRIX / _ROW / _COL on h × o counts.
tb <- tab_of(e)
cm <- suppressWarnings(chisq.test(tb, correct = FALSE))
out("ov_chisq", "0/summary.statistic", cm$statistic)
out("ov_chisq", "0/summary.p_value", cm$p.value)
out("ov_chisq", "0/summary.parameters.df", cm$parameter)
for (i in lh) {
  g <- gof(tb[i, ], colSums(tb) / sum(tb))
  out("ov_chisq", sprintf("1/entry[%s].summary.statistic", i), g$statistic)
  out("ov_chisq", sprintf("1/entry[%s].summary.p_value", i), g$p.value)
}
for (j in lo) {
  g <- gof(tb[, j], rowSums(tb) / sum(tb))
  out("ov_chisq", sprintf("2/entry[%s].summary.statistic", j), g$statistic)
  out("ov_chisq", sprintf("2/entry[%s].summary.p_value", j), g$p.value)
}

# OVERLAY_STD_RESIDUAL: chisq.test()$stdres on the same h × o counts.
sr <- cm$stdres
for (i in lh) for (j in lo) out("ov_std_residual", sprintf("0/cell[%s|%s]", i, j), sr[i, j])

# OVERLAY_SYMMETRY: Bowker on x-bucket × y-bucket counts (GROUP_RANGE
# interval 4 on both axes; a null x has no bucket). mcnemar.test keeps a
# pair nobody moved across (0/0 = NaN), so the kept pairs are summed by
# hand on the same table and cross-checked against mcnemar.test on the
# table those pairs alone span when none is empty.
sx <- e[!is.na(e$x), ]
sb <- function(v) floor(v / 4)
ls <- sort(unique(c(sb(sx$x), sb(sx$y))))
st <- table(factor(sb(sx$x), levels = ls), factor(sb(sx$y), levels = ls))
up <- upper.tri(st)
ss <- (st + t(st))[up]
sd <- (st - t(st))[up]
keep <- ss > 0
sstat <- sum(sd[keep]^2 / ss[keep])
if (all(keep)) stopifnot(isTRUE(all.equal(unname(mcnemar.test(st)$statistic), sstat)))
out("ov_symmetry", "0/summary.statistic", sstat)
out("ov_symmetry", "0/summary.p_value", pchisq(sstat, sum(keep), lower.tail = FALSE))
out("ov_symmetry", "0/summary.parameters.df", sum(keep))

# OVERLAY_FISHER_EXACT_CELL: cell vs the rest of its row / column.
for (i in lh) for (j in lo) {
  a <- tb[i, j]
  m <- matrix(c(a, sum(tb[i, ]) - a, sum(tb[, j]) - a, sum(tb) - sum(tb[i, ]) - sum(tb[, j]) + a), 2, byrow = TRUE)
  out("ov_fisher", sprintf("0/cell[%s|%s]", i, j), fisher.test(m)$p.value)
}

# OVERLAY_PAIRWISE_WELCH_T: x by k (rows), h columns a vs b.
for (r in lk) {
  out("ov_pairwise_welch", sprintf("0/cell[%s|a,b]", r),
      t.test(xs$x[xs$k == r & xs$h == "a"], xs$x[xs$k == r & xs$h == "b"])$p.value)
}

# OVERLAY_PAIRWISE_PROP_Z: share of o = "yes" by h (rows) × k (columns),
# column pairs; n = the cell's row count.
kp <- list(c("p", "q"), c("p", "r"), c("q", "r"))
for (r in lh) for (pr in kp) {
  s <- sapply(pr, function(c) sum(e$h == r & e$k == c & e$o == "yes"))
  n <- sapply(pr, function(c) sum(e$h == r & e$k == c))
  out("ov_pairwise_prop", sprintf("0/cell[%s|%s,%s]", r, pr[1], pr[2]), pz(s, n))
}

# OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z (n_basis weights): y by k, a vs b.
for (r in lk) {
  a <- e$y[e$k == r & e$h == "a"]
  b <- e$y[e$k == r & e$h == "b"]
  z <- (mean(a) - mean(b)) / sqrt(var(a) / length(a) + var(b) / length(b))
  out("ov_pairwise_weighted_z", sprintf("0/cell[%s|a,b]", r), 2 * pnorm(-abs(z)))
}

sub <- e[e$y > 4, ]
sub2 <- e[e$y < 6, ]
xsub <- sub[!is.na(sub$x), ]

# OVERLAY_T_CELL / OVERLAY_Z_CELL: x by k × h, sub (target) vs total.
for (r in lk) for (c in lh) {
  a <- xsub$x[xsub$k == r & xsub$h == c]
  b <- xs$x[xs$k == r & xs$h == c]
  out("ov_compose_means", sprintf("0/cell[%s|%s]", r, c), t.test(a, b)$p.value)
  z <- (mean(a) - mean(b)) / sqrt(var(a) / length(a) + var(b) / length(b))
  out("ov_compose_means", sprintf("1/cell[%s|%s]", r, c), 2 * pnorm(-abs(z)))
}

# OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF on a scalar-mean series: mean x by
# k per slot; spread and n are the spec's params (OV_SERIES_PARAMS in
# the generator).
vt <- 2.5; vr <- 1.75; nt <- 40; nr <- 55
for (r in lk) {
  d <- mean(xsub$x[xsub$k == r]) - mean(xs$x[xs$k == r])
  se <- sqrt(vt / nt + vr / nr)
  df <- (vt / nt + vr / nr)^2 / ((vt / nt)^2 / (nt - 1) + (vr / nr)^2 / (nr - 1))
  out("ov_compose_series", sprintf("0/entry[k=%s].summary.statistic", r), 2 * pt(-abs(d / se), df))
  out("ov_compose_series", sprintf("1/entry[k=%s].summary.statistic", r), 2 * pnorm(-abs(d / se)))
}

# OVERLAY_CHISQ_VS_REF / OVERLAY_PROP_Z_CELL / OVERLAY_PROP_Z_PANEL on
# h × o counts: total (reference), sub and sub2 (targets).
tt <- tab_of(sub)
t2 <- tab_of(sub2)
cv <- gof(as.vector(t(tt)), as.vector(t(tb)) / sum(tb))
out("ov_compose_props", "0/summary.statistic", cv$statistic)
out("ov_compose_props", "0/summary.p_value", cv$p.value)
out("ov_compose_props", "0/summary.parameters.df", cv$parameter)
slots <- list(tb, tt, t2)
for (i in lh) for (j in lo) {
  out("ov_compose_props", sprintf("1/cell[%s|%s]", i, j), pz(c(tt[i, j], tb[i, j]), c(sum(tt[i, ]), sum(tb[i, ]))))
  k <- 0
  for (u in 1:2) for (v in (u + 1):3) {
    a <- slots[[u]]
    b <- slots[[v]]
    out("ov_compose_props", sprintf("2/cell[%s|%s]#%d", i, j, k), pz(c(a[i, j], b[i, j]), c(sum(a[i, ]), sum(b[i, ]))))
    k <- k + 1
  }
}
