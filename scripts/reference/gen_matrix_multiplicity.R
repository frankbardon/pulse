# gen_matrix_multiplicity.R — R oracle for Pulse's matrix multiplicity
# (U28 E3-S4): a MatrixSpec `multiplicity` block corrects a correlation
# matrix's per-pair p-values with stats::p.adjust over the DISTINCT pairs
# — the off-diagonal upper triangle, m = k(k - 1) / 2 for k members; the
# diagonal and the mirrored lower triangle are not separate hypotheses.
#
# Writes <out_dir>/matrix_multiplicity.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_matrix_multiplicity.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. It reads the
# fixture CSVs gen_multivariate.R wrote under <out_dir>/fixtures; base R
# (stats::cor.test, stats::p.adjust) + jsonlite + ppcor (pinned 1.1 by
# gen_multivariate.R) — no new package.
#
# Raw p-values are cor.test's at its defaults (pearson / spearman /
# kendall; frequency weights = the rep() expansion) and ppcor::pcor's
# for a partial correlation (control "all"), the same oracles
# matrix_inference.json pins. Families:
#   - matrix: p.adjust over one result's upper triangle (one family per
#     group bucket on a grouped request);
#   - request: p.adjust over the concatenation of every result's upper
#     triangle (every bucket of a grouped spec, every spec of a request).
# Methods: R "bonferroni", "holm", "BH", "BY" -> Pulse bonferroni, holm,
# bh, by. Adjusted figures are mirrored into both triangles; diagonals
# are null; NA is JSON null.

suppressPackageStartupMessages({
  library(jsonlite)
  library(ppcor)
})

if (!exists("out_dir")) {
  args <- commandArgs(trailingOnly = TRUE)
  out_dir <- if (length(args) >= 1) args[[1]] else "internal/processing/testdata/reference"
  dir.create(out_dir, recursive = TRUE, showWarnings = FALSE)
}
if (!exists("write_golden")) {
  write_golden <- function(name, doc) {
    path <- file.path(out_dir, paste0(name, ".json"))
    txt <- toJSON(doc, auto_unbox = TRUE, json_verbatim = TRUE, pretty = TRUE)
    cat(txt, file = path)
    message("wrote ", path, " (", length(doc$cases), " cases)")
  }
}

mm_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
mm_mat <- function(m) lapply(seq_len(nrow(m)), function(i) lapply(unname(m[i, ]), mm_num))
mm_read <- function(name) {
  read.csv(file.path(out_dir, "fixtures", paste0(name, ".csv")), comment.char = "#")
}

mm_methods <- c(bonferroni = "bonferroni", holm = "holm", bh = "BH", by = "BY")

# One spec's raw p matrix over d (listwise): cor.test per distinct pair,
# or ppcor's partial p-values for MAT_PARTIAL_CORRELATION.
mm_p <- function(d, spec) {
  members <- spec$members
  d <- d[complete.cases(d[, members]), , drop = FALSE]
  k <- length(members)
  P <- matrix(NA_real_, k, k)
  if (spec$type == "MAT_PARTIAL_CORRELATION") {
    P <- unname(pcor(d[, members])$p.value)
    diag(P) <- NA
    return(P)
  }
  idx <- if (spec$weight == "frequency") rep(seq_len(nrow(d)), d$w_freq) else seq_len(nrow(d))
  for (i in seq_len(k - 1)) for (j in (i + 1):k) {
    ct <- suppressWarnings(cor.test(d[[members[i]]][idx], d[[members[j]]][idx], method = spec$method))
    P[i, j] <- ct$p.value
    P[j, i] <- ct$p.value
  }
  P
}

# The upper triangle of P, row-major (the order Pulse files its sites).
mm_upper <- function(P) {
  k <- nrow(P)
  out <- c()
  for (i in seq_len(k - 1)) for (j in (i + 1):k) out <- c(out, P[i, j])
  out
}
# Writes the adjusted upper-triangle values v back into a k x k matrix,
# mirrored, diagonal NA.
mm_fill <- function(k, v) {
  A <- matrix(NA_real_, k, k)
  n <- 0
  for (i in seq_len(k - 1)) for (j in (i + 1):k) {
    n <- n + 1
    A[i, j] <- v[[n]]
    A[j, i] <- v[[n]]
  }
  A
}

mm_cases <- list()
# add_case: specs over fixture fx, optionally grouped by a numeric
# field (bucket per distinct value), corrected under family.
add_case <- function(name, fx, specs, family, group = NULL) {
  df <- mm_read(fx)
  buckets <- if (is.null(group)) list(NULL) else as.list(sort(unique(df[[group]])))
  results <- list()
  for (s in seq_along(specs)) for (b in buckets) {
    d <- if (is.null(b)) df else df[df[[group]] == b, , drop = FALSE]
    results[[length(results) + 1]] <- list(spec = s - 1, bucket = b, P = mm_p(d, specs[[s]]))
  }
  adjusted <- list()
  for (m in names(mm_methods)) {
    if (family == "matrix") {
      adjusted[[m]] <- lapply(results, function(r) mm_mat(mm_fill(nrow(r$P), p.adjust(mm_upper(r$P), method = mm_methods[[m]]))))
    } else {
      pooled <- unlist(lapply(results, function(r) mm_upper(r$P)))
      adj <- p.adjust(pooled, method = mm_methods[[m]])
      out <- list()
      at <- 0
      for (r in results) {
        n <- length(mm_upper(r$P))
        out[[length(out) + 1]] <- mm_mat(mm_fill(nrow(r$P), adj[(at + 1):(at + n)]))
        at <- at + n
      }
      adjusted[[m]] <- out
    }
  }
  m_size <- if (family == "matrix") lapply(results, function(r) length(mm_upper(r$P))) else
    lapply(results, function(r) sum(sapply(results, function(x) length(mm_upper(x$P)))))
  mm_cases[[length(mm_cases) + 1]] <<- list(
    name = name, fixture = fx, family = family,
    group = if (is.null(group)) structure("null", class = "json") else group,
    specs = lapply(specs, function(s) list(type = s$type, method = s$method, members = I(s$members), weight = s$weight)),
    results = lapply(seq_along(results), function(i) {
      r <- results[[i]]
      list(spec = r$spec, bucket = if (is.null(r$bucket)) structure("null", class = "json") else mm_num(r$bucket),
           m = m_size[[i]], p = mm_mat(r$P))
    }),
    adjusted = adjusted
  )
}

spec <- function(type, method, members, weight = "none") list(type = type, method = method, members = members, weight = weight)
mt5 <- c("mpg", "disp", "hp", "wt", "qsec")
att4 <- c("rating", "complaints", "privileges", "learning")

add_case("pearson_mtcars", "mtcars", list(spec("MAT_CORRELATION", "pearson", mt5)), "matrix")
add_case("pearson_mtcars_frequency", "mtcars", list(spec("MAT_CORRELATION", "pearson", c("mpg", "hp", "wt", "drat"), "frequency")), "matrix")
add_case("spearman_attitude", "attitude", list(spec("MAT_CORRELATION", "spearman", c(att4, "raises"))), "matrix")
add_case("kendall_attitude", "attitude", list(spec("MAT_CORRELATION", "kendall", att4)), "matrix")
add_case("partial_attitude", "attitude", list(spec("MAT_PARTIAL_CORRELATION", "pearson", att4)), "matrix")
add_case("grouped_matrix", "mtcars", list(spec("MAT_CORRELATION", "pearson", c("mpg", "disp", "hp", "wt"))), "matrix", group = "am")
add_case("grouped_request", "mtcars", list(spec("MAT_CORRELATION", "pearson", c("mpg", "disp", "hp", "wt"))), "request", group = "am")
add_case("two_specs_request", "mtcars", list(spec("MAT_CORRELATION", "pearson", mt5), spec("MAT_CORRELATION", "spearman", c("mpg", "drat", "wt"))), "request")

write_golden("matrix_multiplicity", list(
  description = "U28 E3-S4 matrix multiplicity: stats::p.adjust (bonferroni, holm, BH, BY) over the off-diagonal upper triangle of cor.test / ppcor p-value matrices, per result (family matrix) or pooled (family request); mirrored, diagonal null.",
  r_version = paste(R.version$major, R.version$minor, sep = "."),
  cases = mm_cases
))
