# gen_overlays.R — R oracle for the inferential overlays (U36 E3-S1).
#
# Each overlay test used to compute its expected p-value with the very
# helper the overlay calls (chiSquareSurvival, kolmogorovSurvival,
# fisherExactTwoSided, statdist.StudentTTwoSidedP, ...), so a wrong
# helper or a wrong argument passed both sides. Every figure below is
# computed by R from the overlay's inputs; the Go tests replay the
# inputs and hold the overlay to R at relative 1e-10. Sourced at the end
# of gen_reference.R (`make reference`), reusing its out_dir,
# write_golden() and num(); it cannot run on its own. Base stats +
# jsonlite only — no new package pin. Deterministic rows, one seeded draw
# (overlay_mean_vs_ref).
#
# Goldens written:
#   * overlay_chisq_vs_pop.json — OVERLAY_CHISQ_VS_POP: chisq.test(x[C],
#     p = pop[C], rescale.p = TRUE) over C, the subset values the
#     population shows (a subset value the population lacks is outside
#     C, the U08 OS-02 rule): statistic, df, p, the smallest expected.
#   * overlay_chisq_vs_ref.json — OVERLAY_CHISQ_VS_REF on 3 x 3 slots:
#     the same goodness of fit with the reference table as the
#     distribution, over C = the cells whose reference count is positive.
#     Stock chisq.test over every cell divides by a zero expected there
#     (NaN for a zero target cell, Inf for a positive one), so the cells
#     outside C are left out and the target is renormalised over C —
#     OVERLAY_CHISQ_VS_POP's rule.
#   * overlay_ks_vs_pop.json — OVERLAY_KS_VS_POP. The histogram path's D
#     is ks.test(x, y)$statistic on the bucketised samples (each record
#     replaced by its bucket index, which is all the histograms keep);
#     the percentile path's D is the largest gap between the two
#     approx(rule = 2) interpolants over the union of the anchors. The
#     p-value is the overlay's documented approximation, Stephens' (en +
#     0.12 + 0.11 / en) correction over the limiting Kolmogorov survival:
#     C_pkolmogorov_two_limit(lambda, lower.tail = FALSE, tol = 1e-300)
#     at lambda = (en + 0.12 + 0.11 / en) D, en = sqrt(m n / (m + n)).
#     ks.test's own p on the bucketised samples (exact conditional on
#     the ties when m n < 10000, else the uncorrected limiting form) is
#     kept beside it (r_ks_test_p, r_ks_test_method,
#     r_ks_test_p_asymptotic) — it is NOT the overlay's figure.
#   * overlay_pairwise_probit_t.json — OVERLAY_PAIRWISE_PROBIT_T, the
#     design formula as documented (a market-research convention, no
#     stock R test): p_i clipped to [1e-10, 1 - 1e-10], t = (qnorm(p_1)
#     - qnorm(p_2)) / sqrt(1 / n_1 + 1 / n_2), df = n_1 + n_2 - 2, p =
#     2 pt(-|t|, df). Pairs (A, B), (A, C), (B, C) of three legs.
#   * overlay_fisher_exact_cell.json — OVERLAY_FISHER_EXACT_CELL: each
#     cell against the rest of its row and column, fisher.test(2 x 2)$
#     p.value (relErr tie rule included).
#   * overlay_two_sample.json — the Welch t and two-means z on summary
#     legs (mean, variance, n) the mean overlays read (OVERLAY_T_CELL /
#     T_VS_REF / Z_CELL / Z_VS_REF / PAIRWISE_WELCH_T): t = (m_a - m_b) /
#     sqrt(v_a / n_a + v_b / n_b), Welch-Satterthwaite df, p = 2 pt(-|t|,
#     df) and 2 pnorm(-|t|); a raw-sample case is cross-checked against
#     t.test. Plus two-proportion cases (the pooled z of
#     OVERLAY_PROP_Z_CELL / _PANEL / PAIRWISE_PROP_Z):
#     prop.test(x, n, correct = FALSE)$p.value. These pin the shared
#     kernels (welchTTest, welchZTest, twoProportionZ) once, so the
#     routing tests that build an expectation by running a kernel on the
#     legs they expect to be read test the routing, not the kernel.
#   * overlay_weighted_unit.json — the weighted-host fixtures of
#     internal/processing/overlay_weighted_host_test.go (legA / legB) and
#     overlay_weighted_contingency_test.go (ctFixture): frequency = stock
#     t.test / chisq.test on the rep()-expanded rows; probability = the
#     same recurrence on w* = w n_eff / sum w (Welch on N* = n_eff with
#     the w* variance; the Pearson table scaled by n_eff / sum w).
#   * overlay_mean_vs_ref.json — OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over
#     an engine AGG_WELFORD series: seeded rows (g, seg, x); slot "total"
#     = every row, slot "sub" = seg "in", both grouped by g. Per group
#     t.test(sub, total)$p.value and the Welch-SE normal tail.

stopifnot(exists("out_dir"), exists("write_golden"), exists("num"))

ov_meta <- function(kind, r_fn, note) {
  list(
    generator = "scripts/reference/gen_overlays.R",
    r_version = R.version.string,
    packages = list(
      stats = as.character(packageVersion("stats")),
      jsonlite = as.character(packageVersion("jsonlite"))
    ),
    primitive = kind,
    r_function = r_fn,
    note = note
  )
}
ov_vec <- function(v) lapply(unname(as.numeric(v)), num)
ov_mat <- function(m) lapply(seq_len(nrow(m)), function(i) ov_vec(m[i, ]))
ov_strs <- function(x) I(unname(as.character(x)))
ov_near <- function(a, b, what, tol = 1e-12) {
  if (abs(a - b) > tol * max(1, abs(a), abs(b))) stop("self-check failed: ", what, ": ", a, " vs ", b)
}

# ----------------------------------------------------- OVERLAY_CHISQ_VS_POP
cvp_case <- function(name, sub, pop, note) {
  C <- names(sub)[names(sub) %in% names(pop)[pop > 0]]
  ct <- suppressWarnings(chisq.test(sub[C], p = pop[C], rescale.p = TRUE))
  list(
    name = name, note = note,
    subset_values = ov_strs(names(sub)), subset_counts = ov_vec(sub),
    pop_values = ov_strs(names(pop)), pop_counts = ov_vec(pop),
    compared = ov_strs(C),
    statistic = num(unname(ct$statistic)), df = num(unname(ct$parameter)),
    p_value = num(ct$p.value), expected_min = num(min(ct$expected))
  )
}
cvp_cases <- list(
  cvp_case("four_category", c(a = 30, b = 30, c = 30, d = 10), c(a = 40, b = 30, c = 20, d = 10),
           "the canonical known answer: X-squared = 7.5 on 3 df"),
  cvp_case("subset_value_absent", c(a = 30, b = 20, c = 10, z = 5), c(a = 60, b = 50, c = 30),
           "z is outside C (the population never shows it); shares renormalise over a, b, c"),
  cvp_case("low_expected", c(a = 50, b = 47, c = 3), c(a = 60, b = 36, c = 4),
           "an expected count below 5 (4): the statistic still stands beside the warning"),
  cvp_case("two_category", c(a = 7, b = 13), c(a = 55, b = 45), "one df"),
  cvp_case("far_tail", c(a = 400, b = 50, c = 50), c(a = 100, b = 100, c = 100),
           "X-squared = 490 on 2 df: p near 1e-107, the survival tail")
)
write_golden("overlay_chisq_vs_pop", c(ov_meta(
  "OVERLAY_CHISQ_VS_POP",
  "chisq.test(x[C], p = pop[C], rescale.p = TRUE)",
  "C = the subset values whose population count is positive."
), list(cases = cvp_cases)))

# ----------------------------------------------------- OVERLAY_CHISQ_VS_REF
cvr_case <- function(name, target, ref, note) {
  tv <- as.vector(t(target))
  rv <- as.vector(t(ref))
  C <- rv > 0
  ct <- suppressWarnings(chisq.test(tv[C], p = rv[C], rescale.p = TRUE))
  list(
    name = name, note = note,
    target = ov_mat(target), ref = ov_mat(ref),
    statistic = num(unname(ct$statistic)), df = num(unname(ct$parameter)),
    p_value = num(ct$p.value), expected_min = num(min(ct$expected)),
    excluded_cells = num(sum(!C))
  )
}
m3 <- function(...) matrix(c(...), 3, 3, byrow = TRUE)
cvr_cases <- list(
  cvr_case("known_answer", m3(10, 20, 30, 40, 50, 60, 70, 80, 90), m3(rep(20, 9)),
           "X-squared = 120 on 8 df"),
  cvr_case("mixed", m3(12, 7, 30, 25, 18, 9, 4, 22, 15), m3(30, 10, 40, 20, 25, 15, 10, 30, 20),
           "an uneven reference distribution"),
  cvr_case("close_fit", m3(31, 9, 42, 19, 27, 14, 11, 28, 21), m3(30, 10, 40, 20, 25, 15, 10, 30, 20),
           "a target close to the reference: p near 1"),
  cvr_case("zero_ref_cell_target_zero", m3(12, 7, 30, 25, 0, 9, 4, 22, 15), m3(30, 10, 40, 20, 0, 15, 10, 30, 20),
           "a zero reference cell the target is also empty in: left out of the statistic and df"),
  cvr_case("zero_ref_cell_target_positive", m3(12, 7, 30, 25, 6, 9, 4, 22, 15), m3(30, 10, 40, 20, 0, 15, 10, 30, 20),
           "a zero reference cell holding 6 target counts: outside C, the target renormalised over C (stock chisq.test is Inf here)")
)
write_golden("overlay_chisq_vs_ref", c(ov_meta(
  "OVERLAY_CHISQ_VS_REF",
  "chisq.test(target[C], p = ref[C], rescale.p = TRUE), cells read row by row",
  "C = the cells whose reference count is positive."
), list(cases = cvr_cases)))

# ------------------------------------------------------- OVERLAY_KS_VS_POP
kolmo_surv <- function(l) .Call(stats:::C_pkolmogorov_two_limit, l, FALSE, 1e-300)
stephens_p <- function(D, m, n) {
  en <- sqrt(m * n / (m + n))
  kolmo_surv((en + 0.12 + 0.11 / en) * D)
}
ksh_case <- function(name, lo, hi, host_bins, pop_bins, note) {
  x <- rep(seq_along(host_bins), host_bins)
  y <- rep(seq_along(pop_bins), pop_bins)
  m <- length(x); n <- length(y)
  kt <- suppressWarnings(ks.test(x, y))
  ka <- suppressWarnings(ks.test(x, y, exact = FALSE))
  # The bucket-edge gap, exactly; ks.test's own D accumulates its
  # 1/m - 1/n steps (a few ulps off), so it is the cross-check.
  D <- max(abs(cumsum(host_bins) / m - cumsum(pop_bins) / n))
  ov_near(D, unname(kt$statistic), paste(name, "ks.test D"))
  list(
    name = name, note = note, path = "histogram",
    min = num(lo), max = num(hi), host_bins = ov_vec(host_bins), pop_bins = ov_vec(pop_bins),
    n_subset = num(m), n_pop = num(n),
    statistic = num(D), p_value = num(stephens_p(D, m, n)),
    r_ks_test_p = num(kt$p.value), r_ks_test_method = kt$method,
    r_ks_test_p_asymptotic = num(ka$p.value)
  )
}
ksp_case <- function(name, host_pct, pop_pct, m, n, note) {
  cdf <- function(pct) {
    v <- unname(pct)
    o <- order(v)
    list(x = v[o], y = as.numeric(sub("^p", "", names(pct)))[o] / 100)
  }
  hc <- cdf(host_pct); pc <- cdf(pop_pct)
  anchors <- sort(c(hc$x, pc$x))
  gap <- abs(approx(hc$x, hc$y, xout = anchors, rule = 2)$y - approx(pc$x, pc$y, xout = anchors, rule = 2)$y)
  D <- max(gap)
  list(
    name = name, note = note, path = "percentile",
    host_percentiles = lapply(as.list(host_pct), num), pop_percentiles = lapply(as.list(pop_pct), num),
    n_subset = num(m), n_pop = num(n),
    statistic = num(D), p_value = num(stephens_p(D, m, n))
  )
}
ks_ov_cases <- list(
  ksh_case("disjoint_halves", 0, 10, c(20, 20, 20, 20, 20, 0, 0, 0, 0, 0), c(0, 0, 0, 0, 0, 20, 20, 20, 20, 20),
           "D = 1: no overlap; the Stephens p is near 1e-45 (ks.test underflows to 0)"),
  ksh_case("shifted_small", 0, 10, c(5, 8, 12, 10, 7, 4, 2, 1, 1, 0), c(3, 6, 10, 14, 12, 9, 6, 4, 3, 2),
           "m n = 3450 < 10000: ks.test's p is the exact conditional one, far from Stephens'"),
  ksh_case("shifted_large", 0, 10, c(50, 80, 120, 100, 70, 40, 20, 10, 10, 0),
           c(300, 600, 1000, 1400, 1200, 900, 600, 400, 300, 200),
           "the same shapes at 10x / 100x: the far tail"),
  ksh_case("moderate", -3, 3, c(10, 14, 20, 16, 8, 5), c(6, 12, 18, 20, 14, 10),
           "a middling p")
)
ks_ov_cases <- c(ks_ov_cases, list(
  ksp_case("percentile_shifted", c(p25 = 10, p50 = 20, p75 = 30), c(p25 = 15, p50 = 25, p75 = 35), 100, 100,
           "quartiles shifted by 5: D = 0.125"),
  ksp_case("percentile_deciles", c(p10 = 2.1, p25 = 3.4, p50 = 5.2, p75 = 7.7, p90 = 9.9),
           c(p10 = 1.6, p25 = 3.9, p50 = 6.1, p75 = 8.05, p90 = 11.3), 80, 500,
           "uneven anchors on both arms, unequal sizes")
))
write_golden("overlay_ks_vs_pop", c(ov_meta(
  "OVERLAY_KS_VS_POP",
  "D: max |cumsum(host) / m - cumsum(pop) / n| = ks.test(bucketised x, bucketised y)$statistic (histogram) or the approx(rule = 2) gap (percentile); p: C_pkolmogorov_two_limit((en + 0.12 + 0.11/en) D, FALSE, 1e-300)",
  "p_value is Stephens' corrected asymptotic, the overlay's documented approximation; r_ks_test_* is ks.test's own p on the bucketised samples, kept for comparison only."
), list(cases = ks_ov_cases)))

# ----------------------------------------------- OVERLAY_PAIRWISE_PROBIT_T
probit_clip <- function(p) pmin(pmax(p, 1e-10), 1 - 1e-10)
probit_pair <- function(p1, n1, p2, n2) {
  tt <- (qnorm(probit_clip(p1)) - qnorm(probit_clip(p2))) / sqrt(1 / n1 + 1 / n2)
  df <- n1 + n2 - 2
  list(t = num(tt), df = num(df), p_value = num(2 * pt(-abs(tt), df)))
}
probit_case <- function(name, pct, n, note) {
  p <- pct / 100
  pairs <- list(c(1, 2), c(1, 3), c(2, 3))
  list(
    name = name, note = note, pct = ov_vec(pct), n = ov_vec(n),
    pairs = lapply(pairs, function(ij) probit_pair(p[ij[1]], n[ij[1]], p[ij[2]], n[ij[2]]))
  )
}
probit_cases <- list(
  probit_case("equal_n", c(50, 30, 10), c(100, 100, 100), "legs A, B, C of 100 each"),
  probit_case("unequal_n", c(62.5, 41, 12.3), c(40, 125, 77), "uneven legs"),
  probit_case("clipped_ends", c(0, 100, 37), c(30, 30, 60), "0% and 100% clip to 1e-10 / 1 - 1e-10 before qnorm"),
  probit_case("close_shares", c(48, 51, 49.5), c(400, 380, 410), "near-equal shares: p near 1")
)
write_golden("overlay_pairwise_probit_t", c(ov_meta(
  "OVERLAY_PAIRWISE_PROBIT_T",
  "t = (qnorm(clip(p1)) - qnorm(clip(p2))) / sqrt(1/n1 + 1/n2); 2 * pt(-|t|, n1 + n2 - 2)",
  "The design formula, pinned as is (no stock R test computes it): clip = [1e-10, 1 - 1e-10]; p = pct / 100; pairs (A,B), (A,C), (B,C)."
), list(cases = probit_cases)))

# ----------------------------------------------- OVERLAY_FISHER_EXACT_CELL
fisher_cell_case <- function(name, tb, note) {
  N <- sum(tb)
  p <- matrix(NA_real_, nrow(tb), ncol(tb))
  for (i in seq_len(nrow(tb))) for (j in seq_len(ncol(tb))) {
    a <- tb[i, j]
    rs <- sum(tb[i, ])
    cs <- sum(tb[, j])
    p[i, j] <- fisher.test(matrix(c(a, rs - a, cs - a, N - rs - cs + a), 2, byrow = TRUE))$p.value
  }
  list(name = name, note = note, table = ov_mat(tb), p = ov_mat(p))
}
fisher_cell_cases <- list(
  fisher_cell_case("small_textbook_2x2", matrix(c(1, 9, 11, 3), 2, byrow = TRUE),
                   "cell (r0, c0) is the 2 x 2 {1, 9; 11, 3}"),
  fisher_cell_case("zero_cell_2x2", matrix(c(0, 10, 12, 2), 2, byrow = TRUE), "a zero cell"),
  fisher_cell_case("three_by_three", matrix(c(3, 8, 2, 6, 1, 9, 4, 5, 7), 3, byrow = TRUE), "small counts"),
  fisher_cell_case("two_by_three", matrix(c(25, 12, 40, 18, 30, 22), 2, byrow = TRUE), "larger counts")
)
write_golden("overlay_fisher_exact_cell", c(ov_meta(
  "OVERLAY_FISHER_EXACT_CELL",
  "fisher.test(matrix(c(a, row - a, col - a, N - row - col + a), 2, byrow = TRUE))$p.value per cell",
  "Each cell against the rest of its row and column."
), list(cases = fisher_cell_cases)))

# --------------------------------------------------- summary-leg two-sample
ts_case <- function(name, ma, va, na, mb, vb, nb, note, raw = NULL) {
  sa <- va / na; sb <- vb / nb
  tt <- (ma - mb) / sqrt(sa + sb)
  df <- (sa + sb)^2 / (sa^2 / (na - 1) + sb^2 / (nb - 1))
  p <- 2 * pt(-abs(tt), df)
  out <- list(
    name = name, note = note, kind = "means",
    mean_a = num(ma), var_a = num(va), n_a = num(na),
    mean_b = num(mb), var_b = num(vb), n_b = num(nb),
    t = num(tt), df = num(df), welch_p = num(p), z_p = num(2 * pnorm(-abs(tt)))
  )
  if (!is.null(raw)) {
    ov_near(p, t.test(raw$a, raw$b)$p.value, paste(name, "t.test"))
    out$a <- ov_vec(raw$a)
    out$b <- ov_vec(raw$b)
  }
  out
}
tp_case <- function(name, x, n, note) {
  list(name = name, note = note, kind = "proportions",
       x = ov_vec(x), n = ov_vec(n),
       prop_p = num(suppressWarnings(prop.test(x, n, correct = FALSE))$p.value))
}
ts_raw_a <- c(1, 2, 4)
ts_raw_b <- c(3, 5, 6, 10)
ts_cases <- list(
  ts_case("unit_t1_df2", 10, 1, 2, 9, 1, 2, "t = 1 on 2 df: p = 1 - 1/sqrt(3)"),
  ts_case("pairwise_welch", 10, 4, 50, 12, 9, 60, "the pairwise Welch leg pair"),
  ts_case("z_cell", 10, 4, 10, 9, 4, 10, "the Z_CELL parity legs"),
  ts_case("raw_samples", mean(ts_raw_a), var(ts_raw_a), 3, mean(ts_raw_b), var(ts_raw_b), 4,
          "A = {1, 2, 4}, B = {3, 5, 6, 10}: cross-checked against t.test",
          raw = list(a = ts_raw_a, b = ts_raw_b)),
  tp_case("prop_50_30", c(50, 30), c(100, 100), "p1 = 0.50, p2 = 0.30, n = 100 each"),
  tp_case("prop_50_100_60_80", c(50, 60), c(100, 80), "unequal bases"),
  tp_case("prop_40_80_30_50", c(40, 30), c(80, 50), "unequal bases"),
  tp_case("prop_25_160_20_120", c(25, 20), c(160, 120), "small shares, near-equal")
)
write_golden("overlay_two_sample", c(ov_meta(
  "two-sample overlay legs",
  "Welch: 2 * pt(-|t|, df) and 2 * pnorm(-|t|) on summary legs; proportions: prop.test(x, n, correct = FALSE)$p.value",
  "kind means: (mean, variance, n) legs, t = (mean_a - mean_b) / sqrt(var_a/n_a + var_b/n_b); kind proportions: x successes of n."
), list(cases = ts_cases)))

# ---------------------------------------------------- weighted unit fixtures
wleg_a <- list(x = c(3, 7, 4, 9, 12, 5, 8), w = c(1, 3, 2, 1, 4, 2, 1))
wleg_b <- list(x = c(6, 11, 9, 14, 10, 13), w = c(2, 1, 3, 2, 1, 2))
wleg <- function(l, kind) {
  sw <- sum(l$w)
  nstar <- if (kind == "probability") sw^2 / sum(l$w^2) else sw
  m <- sum(l$w * l$x) / sw
  ws <- l$w * nstar / sw
  list(mean = m, var = sum(ws * (l$x - m)^2) / (nstar - 1), nstar = nstar)
}
wu_cases <- list()
for (kind in c("frequency", "probability")) {
  a <- wleg(wleg_a, kind)
  b <- wleg(wleg_b, kind)
  sa <- a$var / a$nstar; sb <- b$var / b$nstar
  tt <- (a$mean - b$mean) / sqrt(sa + sb)
  df <- (sa + sb)^2 / (sa^2 / (a$nstar - 1) + sb^2 / (b$nstar - 1))
  tp <- 2 * pt(-abs(tt), df)
  if (kind == "frequency") {
    ov_near(tp, t.test(rep(wleg_a$x, wleg_a$w), rep(wleg_b$x, wleg_b$w))$p.value, "legA/legB frequency t.test")
  }
  wu_cases[[length(wu_cases) + 1]] <- list(name = "legs_welch_t", kind = kind, statistic = num(tt), df = num(df), p_value = num(tp))
  wu_cases[[length(wu_cases) + 1]] <- list(name = "legs_welch_z", kind = kind, statistic = num(tt), p_value = num(2 * pnorm(-abs(tt))))
}
# ctFixture (row, column, weight) — overlay_weighted_contingency_test.go.
ct_rec <- matrix(c(
  0, 0, 1, 0, 0, 9, 0, 0, 2,
  0, 1, 3, 0, 1, 1,
  0, 2, 8, 0, 2, 1, 0, 2, 1, 0, 2, 2,
  1, 0, 2, 1, 0, 2,
  1, 1, 10, 1, 1, 1, 1, 1, 1,
  1, 2, 1, 1, 2, 3
), ncol = 3, byrow = TRUE)
ct_sw <- matrix(0, 2, 3)
for (k in seq_len(nrow(ct_rec))) {
  i <- ct_rec[k, 1] + 1; j <- ct_rec[k, 2] + 1
  ct_sw[i, j] <- ct_sw[i, j] + ct_rec[k, 3]
}
ct_neff <- sum(ct_rec[, 3])^2 / sum(ct_rec[, 3]^2)
for (kind in c("frequency", "probability")) {
  tab <- if (kind == "probability") ct_sw * ct_neff / sum(ct_sw) else ct_sw
  cm <- suppressWarnings(chisq.test(tab, correct = FALSE))
  if (kind == "frequency") {
    ex <- ct_rec[rep(seq_len(nrow(ct_rec)), ct_rec[, 3]), ]
    ov_near(unname(cm$statistic), unname(suppressWarnings(chisq.test(table(ex[, 1], ex[, 2]), correct = FALSE))$statistic),
            "ctFixture expansion")
  }
  wu_cases[[length(wu_cases) + 1]] <- list(name = "ct_chisq_matrix", kind = kind,
                                           statistic = num(unname(cm$statistic)), df = num(unname(cm$parameter)),
                                           p_value = num(cm$p.value))
}
write_golden("overlay_weighted_unit", c(ov_meta(
  "weighted overlay unit fixtures",
  "frequency: t.test / chisq.test on the rep() expansion; probability: Welch on N* = n_eff with the w* variance, chisq.test on the table scaled by n_eff / sum w",
  "legA / legB: overlay_weighted_host_test.go; ctFixture: overlay_weighted_contingency_test.go. legs_welch_* is legA (target) against legB (reference)."
), list(cases = wu_cases)))

# --------------------------------------- T / Z vs ref on an AGG_WELFORD series
set.seed(36301)
mvr_g <- rep(c("east", "north", "west"), times = c(22, 18, 25))
mvr_seg <- sample(c("in", "out"), length(mvr_g), replace = TRUE, prob = c(0.45, 0.55))
mvr_x <- round(rnorm(length(mvr_g), mean = c(east = 10, north = 12, west = 9)[mvr_g], sd = 2.5) +
                 ifelse(mvr_seg == "in", 0.8, 0), 2)
mvr_groups <- list()
for (g in c("east", "north", "west")) {
  a <- mvr_x[mvr_g == g & mvr_seg == "in"]
  b <- mvr_x[mvr_g == g]
  z <- (mean(a) - mean(b)) / sqrt(var(a) / length(a) + var(b) / length(b))
  mvr_groups[[length(mvr_groups) + 1]] <- list(
    g = g, n_sub = num(length(a)), n_total = num(length(b)),
    t_p = num(t.test(a, b)$p.value), z_p = num(2 * pnorm(-abs(z)))
  )
}
write_golden("overlay_mean_vs_ref", c(ov_meta(
  "OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over AGG_WELFORD",
  "per group: t.test(x[sub], x[total])$p.value; 2 * pnorm(-|z|), z on the Welch standard error",
  "Slot total = every row, slot sub = seg \"in\" (the reference / target); both grouped by g, scoring AGG_WELFORD(x)."
), list(cases = list(list(
  name = "seeded_three_groups",
  g = ov_strs(mvr_g), seg = ov_strs(mvr_seg), x = ov_vec(mvr_x),
  groups = mvr_groups
)))))
