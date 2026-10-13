# gen_correspondence.R — R oracle for Pulse's OVERLAY_CORRESPONDENCE (U28 E1-S3).
#
# Writes <out_dir>/correspondence.json. Sourced at the end of
# gen_reference.R (`make reference`), reusing its out_dir and
# write_golden(); runnable on its own too:
#
#   Rscript scripts/reference/gen_correspondence.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Needs the
# `ca` package (install.packages("ca", repos = "https://cloud.r-project.org"))
# plus jsonlite.
#
# Per case, over an r x c contingency table x (simple CA, ca::ca):
#   * sv, inertia = sv^2 over every non-trivial dimension
#     (min(r, c) - 1), total inertia = sum(inertia)
#   * rowmass / colmass, rowcoord / colcoord (STANDARD coordinates)
#   * for each normalization (symmetric, row_principal,
#     column_principal) the first `dimensions` columns of the map
#     coordinates: principal = standard * sv; symmetric = both
#     principal, row_principal = principal rows + standard columns,
#     column_principal = standard rows + principal columns — checked
#     against plot.ca(map = ...) on dimensions 1-2
#   * quality = sum over the retained dimensions of cos^2
#     (principal coordinate^2 / rowdist^2), contribution = mass *
#     principal coordinate^2 / inertia (fractions, not per mil) —
#     checked against summary.ca's per-mil qlt / ctr
# ca's own sign is left as is: the Go test normalizes each dimension
# to the linalg.SVD convention (the dominant component of
# V = sqrt(colmass) * standard column coordinate positive) before
# comparing. A case with zero-margin rows / columns hands ca the table
# WITHOUT them (`kept_rows` / `kept_cols`, 0-based): Pulse drops them.

suppressPackageStartupMessages({
  library(jsonlite)
  library(ca)
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

ca_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
ca_vec <- function(v) lapply(as.numeric(v), ca_num)
ca_mat <- function(m) {
  m <- as.matrix(m)
  lapply(seq_len(nrow(m)), function(i) lapply(as.numeric(m[i, ]), ca_num))
}

ca_case <- function(name, x, note, dimensions = 2) {
  x <- as.matrix(x)
  dimnames(x) <- NULL
  kept_rows <- which(rowSums(x) > 0)
  kept_cols <- which(colSums(x) > 0)
  xr <- x[kept_rows, kept_cols, drop = FALSE]
  dimnames(xr) <- list(paste0("r", kept_rows), paste0("c", kept_cols))
  fit <- ca(xr)
  K <- min(dim(xr)) - 1
  sv <- fit$sv[seq_len(K)]
  d <- dimensions
  rp <- sweep(fit$rowcoord[, seq_len(d), drop = FALSE], 2, sv[seq_len(d)], "*")
  cp <- sweep(fit$colcoord[, seq_len(d), drop = FALSE], 2, sv[seq_len(d)], "*")
  rs <- fit$rowcoord[, seq_len(d), drop = FALSE]
  cs <- fit$colcoord[, seq_len(d), drop = FALSE]
  maps <- list(
    symmetric = list(rows = rp, cols = cp),
    row_principal = list(rows = rp, cols = cs),
    column_principal = list(rows = rs, cols = cp)
  )
  if (d >= 2) {
    grDevices::pdf(NULL)
    for (m in names(maps)) {
      ca_map <- c(symmetric = "symmetric", row_principal = "rowprincipal", column_principal = "colprincipal")[[m]]
      p <- plot(fit, map = ca_map)
      stopifnot(isTRUE(all.equal(unname(p$rows), unname(maps[[m]]$rows[, 1:2]), tolerance = 1e-12)))
      stopifnot(isTRUE(all.equal(unname(p$cols), unname(maps[[m]]$cols[, 1:2]), tolerance = 1e-12)))
    }
    grDevices::dev.off()
  }
  rquality <- rowSums(rp^2) / fit$rowdist^2
  cquality <- rowSums(cp^2) / fit$coldist^2
  rctr <- sweep(fit$rowmass * rp^2, 2, sv[seq_len(d)]^2, "/")
  cctr <- sweep(fit$colmass * cp^2, 2, sv[seq_len(d)]^2, "/")
  # summary.ca columns: name, mass, qlt, inr, k=1, cor, ctr, ... (per
  # mil) over the first two dimensions — summary.ca reports two (ca 0.72
  # fails on summary(ca(x, nd = ...))), so the check runs when d = 2.
  if (d == 2) {
    s <- summary(fit)
    stopifnot(max(abs(s$rows[[3]] - round(1000 * rquality))) <= 1)
    stopifnot(max(abs(s$columns[[3]] - round(1000 * cquality))) <= 1)
    stopifnot(max(abs(s$rows[[7]] - round(1000 * rctr[, 1]))) <= 1)
    stopifnot(max(abs(s$columns[[7]] - round(1000 * cctr[, 1]))) <= 1)
  }
  list(
    name = name,
    note = note,
    dimensions = d,
    observed = ca_mat(x),
    kept_rows = as.list(kept_rows - 1),
    kept_cols = as.list(kept_cols - 1),
    singular_values = ca_vec(sv),
    inertia = ca_vec(sv^2),
    total_inertia = ca_num(sum(sv^2)),
    row_mass = ca_vec(fit$rowmass),
    col_mass = ca_vec(fit$colmass),
    row_standard = ca_mat(fit$rowcoord[, seq_len(K), drop = FALSE]),
    col_standard = ca_mat(fit$colcoord[, seq_len(K), drop = FALSE]),
    coordinates = lapply(maps, function(m) list(rows = ca_mat(m$rows), cols = ca_mat(m$cols))),
    row_quality = ca_vec(rquality),
    col_quality = ca_vec(cquality),
    row_contribution = ca_mat(rctr),
    col_contribution = ca_mat(cctr)
  )
}

ca_cases <- list()

data(smoke, package = "ca")
ca_cases[[length(ca_cases) + 1]] <- ca_case(
  "greenacre_smoke",
  as.matrix(smoke),
  "Greenacre's smoking data (staff group x smoking category, ca::smoke); published principal inertias 0.0748, 0.0100, 0.0004, total 0.0852."
)

set.seed(2803)
ca_cases[[length(ca_cases) + 1]] <- ca_case(
  "seeded_6x5_3d",
  matrix(rpois(30, lambda = rep(c(40, 12, 25, 8, 33, 19), 5)) + 1, nrow = 6),
  "Seeded Poisson counts (+1), set.seed(2803); three retained dimensions.",
  dimensions = 3
)

ca_cases[[length(ca_cases) + 1]] <- ca_case(
  "zero_margins_5x4",
  matrix(c(
    12, 0, 7, 30,
    0, 0, 0, 0,
    25, 0, 4, 9,
    8, 0, 16, 11,
    3, 0, 21, 6
  ), nrow = 5, byrow = TRUE),
  "Row 2 and column 2 have zero totals: Pulse drops them (one PULSE_OVERLAY_REF_ZERO per axis); ca sees the 4 x 3 remainder."
)

write_golden("correspondence", list(
  generator = "scripts/reference/gen_correspondence.R",
  r_version = R.version.string,
  packages = list(
    ca = as.character(packageVersion("ca")),
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_CORRESPONDENCE",
  r_function = "ca::ca(x): sv, rowmass / colmass, rowcoord / colcoord (standard); principal = standard * sv; quality / contribution from rowdist / mass",
  note = "Signs are ca's own; normalize per dimension to the linalg.SVD convention (dominant component of sqrt(colmass) * standard column coordinate positive) before comparing.",
  cases = ca_cases
))
