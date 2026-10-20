# gen_adstock.R — R oracle for WIN_ADSTOCK (custom-regressions E3-S3):
# the lag kernel of each kernel (geometric, Weibull CDF, Weibull PDF),
# normalised and raw, and the adstocked series it produces over a
# two-partition series with nulls.
#
# Writes <out_dir>/adstock.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_adstock.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Base stats +
# jsonlite only.
#
# Kernel, for lags l = 0..max_lag (max_lag + 1 weights):
#   geometric:   decay^l
#   weibull_cdf: pweibull(l, shape, scale, lower.tail = FALSE)  (survival)
#   weibull_pdf: dweibull(l + 1, shape, scale)                  (one lag on)
# normalised: w / sum(w).
#
# Series: per partition, in order, y_i = sum_l w_l * x_{i-l}, computed by
# stats::filter(method = "convolution", sides = 1) over the series with
# max_lag zeros prepended (a lag before the partition start adds 0) and
# NA replaced by 0 (a null at a lagged row adds 0); y_i is NA (null)
# where x_i is NA. Pulse runs its own loop, so the two never share code.

suppressPackageStartupMessages(library(jsonlite))

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

ad_num <- function(x) {
  if (length(x) != 1) stop("ad_num() takes a scalar")
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("non-finite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
ad_vec <- function(v) lapply(unname(as.numeric(v)), ad_num)

ad_kernel <- function(kernel, max_lag, decay = NA, shape = NA, scale = NA, normalize = TRUE) {
  l <- 0:max_lag
  w <- switch(kernel,
    geometric = decay^l,
    weibull_cdf = pweibull(l, shape = shape, scale = scale, lower.tail = FALSE),
    weibull_pdf = dweibull(l + 1, shape = shape, scale = scale),
    stop("unknown kernel ", kernel))
  if (normalize) w <- w / sum(w)
  w
}

ad_series <- function(x, w) {
  L <- length(w) - 1
  x0 <- ifelse(is.na(x), 0, x)
  y <- as.numeric(stats::filter(c(rep(0, L), x0), w, method = "convolution", sides = 1))
  y <- y[-seq_len(L)]
  y[is.na(x)] <- NA
  y
}

# Two partitions of twelve ordered periods. Partition "a" has a null
# mid-series; partition "b" opens with a null and has one later.
ad_x <- list(
  a = c(120, 80, 0, 200, NA, 150, 90, 60, 300, 10, 0, 45),
  b = c(NA, 40, 55, 0, 70, 220, 35, NA, 95, 130, 5, 60))

ad_cases <- list()
ad_case <- function(name, kernel, max_lag, decay = NA, shape = NA, scale = NA, normalize = TRUE) {
  w <- ad_kernel(kernel, max_lag, decay, shape, scale, normalize)
  params <- list(kernel = kernel, max_lag = max_lag, normalize = normalize)
  if (!is.na(decay)) params$decay <- ad_num(decay)
  if (!is.na(shape)) params$shape <- ad_num(shape)
  if (!is.na(scale)) params$scale <- ad_num(scale)
  parts <- lapply(names(ad_x), function(g) list(
    partition = g, x = ad_vec(ad_x[[g]]), y = ad_vec(ad_series(ad_x[[g]], w))))
  ad_cases[[length(ad_cases) + 1]] <<- list(
    name = name, params = params, weights = ad_vec(w), partitions = parts)
}

for (norm in c(TRUE, FALSE)) {
  tag <- if (norm) "normalized" else "raw"
  ad_case(paste0("geometric_", tag), "geometric", 4, decay = 0.6, normalize = norm)
  ad_case(paste0("weibull_cdf_", tag), "weibull_cdf", 6, shape = 2, scale = 3, normalize = norm)
  ad_case(paste0("weibull_pdf_", tag), "weibull_pdf", 6, shape = 1.5, scale = 2.5, normalize = norm)
  ad_case(paste0("weibull_pdf_shape_below_one_", tag), "weibull_pdf", 3, shape = 0.7, scale = 4, normalize = norm)
}
ad_case("geometric_long_lag", "geometric", 15, decay = 0.35)
ad_case("geometric_decay_zero_identity", "geometric", 2, decay = 0)

adstock_doc <- list(
  generator = "scripts/reference/gen_adstock.R (via gen_reference.R)",
  r_version = R.version.string,
  packages = list(stats = as.character(packageVersion("stats")),
                  jsonlite = as.character(packageVersion("jsonlite"))),
  primitive = "WIN_ADSTOCK: geometric / Weibull CDF (survival) / Weibull PDF (at l + 1) lag kernels and their per-partition convolution",
  r_function = "decay^l; pweibull(l, shape, scale, lower.tail = FALSE); dweibull(l + 1, shape, scale); stats::filter(c(rep(0, L), x0), w, method = 'convolution', sides = 1)",
  note = paste(
    "Lags l = 0..max_lag; normalised kernels are w / sum(w). A lag before the partition start and a null lagged value add 0;",
    "a row whose own value is null reads null. Partitions are given in order_by order."),
  cases = ad_cases)
write_golden("adstock", adstock_doc)
