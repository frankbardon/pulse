# gen_markov.R — R oracle for Pulse's OVERLAY_MARKOV (U28 E2-S2).
#
# Writes <out_dir>/markov.json. Sourced at the end of gen_reference.R
# (`make reference`), reusing its out_dir and write_golden(); runnable on
# its own too:
#
#   Rscript scripts/reference/gen_markov.R internal/processing/testdata/reference
#
# (then append the `// golden-hash:` footer the Makefile's reference
# target writes). CI never runs R: the golden is committed. Needs the
# `markovchain` package (install.packages("markovchain", repos =
# "https://cloud.r-project.org"); its igraph / expm dependencies need the
# Homebrew Fortran workaround in docs/src/internals/regenerating-goldens.md)
# plus jsonlite.
#
# Per case, over a square count table x of from-state (row) x to-state
# (column) transitions, states aligned by name:
#   * transition: x / rowSums(x); a zero row becomes the unit row (the
#     state is absorbing) — Pulse's zero-row rule
#   * step_<n>: P^n (expm::`%^%` through markovchain's `^`) per steps
#   * initial: "margin" (rowSums(x) / sum(x)) or a named share map,
#     normalized; distribution_<n> = initial %*% P^n
#   * irreducible / period / communicating classes
#     (markovchain::is.irreducible, period, communicatingClasses)
#   * steady_state = steadyStates() when the chain is irreducible and
#     aperiodic (Pulse emits it only then), else null
#   * absorbing states (declared = the chain's absorbing states here, so
#     markovchain's transient -> recurrent time equals Pulse's time to the
#     declared set): meanAbsorptionTime(), absorptionProbabilities()

suppressPackageStartupMessages({
  library(jsonlite)
  library(markovchain)
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

mk_num <- function(x) {
  if (is.na(x)) return(structure("null", class = "json"))
  if (!is.finite(x)) stop("infinite reference value: ", x)
  structure(sprintf("%.17g", x), class = "json")
}
mk_vec <- function(v) lapply(as.numeric(v), mk_num)
mk_mat <- function(m) {
  m <- as.matrix(m)
  lapply(seq_len(nrow(m)), function(i) lapply(as.numeric(m[i, ]), mk_num))
}
mk_named <- function(v) {
  out <- lapply(as.numeric(v), mk_num)
  names(out) <- names(v)
  out
}

mk_case <- function(name, states, x, note, steps = c(1, 3, 6, 12), initial = "margin", absorbing = character()) {
  x <- matrix(as.numeric(x), nrow = length(states), byrow = TRUE, dimnames = list(states, states))
  rs <- rowSums(x)
  P <- x
  for (i in seq_along(states)) {
    if (rs[i] > 0) {
      P[i, ] <- x[i, ] / rs[i]
    } else {
      P[i, ] <- 0
      P[i, i] <- 1
    }
  }
  mc <- new("markovchain", states = states, transitionMatrix = P, name = name)
  if (identical(initial, "margin")) {
    init <- rs / sum(rs)
  } else {
    init <- setNames(numeric(length(states)), states)
    init[names(initial)] <- unlist(initial)
    init <- init / sum(init)
  }
  step_mats <- list()
  dists <- list()
  for (n in steps) {
    Pn <- (mc^n)@transitionMatrix
    step_mats[[as.character(n)]] <- mk_mat(Pn)
    dists[[as.character(n)]] <- mk_vec(as.numeric(init %*% Pn))
  }
  irr <- is.irreducible(mc)
  per <- if (irr) period(mc) else 0
  json_null <- structure("null", class = "json")
  steady <- json_null
  if (irr && per == 1) {
    ss <- steadyStates(mc)
    stopifnot(nrow(ss) == 1)
    steady <- mk_vec(ss[1, states])
  }
  abs_time <- json_null
  abs_prob <- json_null
  if (length(absorbing) > 0) {
    stopifnot(setequal(absorbing, absorbingStates(mc)))
    abs_time <- mk_named(meanAbsorptionTime(mc))
    ap <- absorptionProbabilities(mc)
    abs_prob <- lapply(rownames(ap), function(r) mk_named(setNames(ap[r, ], colnames(ap))))
    names(abs_prob) <- rownames(ap)
  }
  list(
    name = name,
    note = note,
    states = as.list(states),
    counts = mk_mat(x),
    transition = mk_mat(P),
    steps = as.list(as.integer(steps)),
    step_matrices = step_mats,
    initial = if (identical(initial, "margin")) "margin" else initial,
    initial_distribution = mk_vec(init),
    distributions = dists,
    irreducible = irr,
    period = as.integer(per),
    communicating_classes = lapply(communicatingClasses(mc), as.list),
    steady_state = steady,
    absorbing = as.list(absorbing),
    mean_absorption_time = abs_time,
    absorption_probabilities = abs_prob
  )
}

mk_cases <- list()

mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "tier_migration_ergodic",
  c("bronze", "silver", "gold", "platinum"),
  c(
    120, 30, 6, 1,
    25, 140, 28, 4,
    5, 22, 160, 18,
    1, 3, 20, 90
  ),
  "Irreducible, aperiodic tier migration: steady state, steps 1/3/6/12 from the observed row margin."
)

mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "tier_migration_initial_map",
  c("bronze", "silver", "gold", "platinum"),
  c(
    120, 30, 6, 1,
    25, 140, 28, 4,
    5, 22, 160, 18,
    1, 3, 20, 90
  ),
  "The ergodic table projected from a key -> share map (normalized: 3 bronze, 1 silver).",
  steps = c(2, 24),
  initial = list(bronze = 3, silver = 1)
)

mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "churn_absorbing",
  c("bronze", "silver", "gold", "churned"),
  c(
    150, 40, 5, 30,
    30, 160, 30, 20,
    4, 25, 180, 10,
    0, 0, 0, 55
  ),
  "Churned only ever stays churned: absorbing, so the chain is reducible (no steady state); mean time to churn per tier.",
  absorbing = c("churned")
)

mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "periodic_bipartite",
  c("a", "b", "c", "d"),
  c(
    0, 0, 7, 3,
    0, 0, 2, 8,
    5, 5, 0, 0,
    1, 9, 0, 0
  ),
  "Irreducible with period 2 ({a, b} <-> {c, d}): no steady state."
)

mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "zero_row_absorbing",
  c("trial", "active", "lapsed", "closed"),
  c(
    10, 30, 5, 5,
    0, 80, 15, 5,
    0, 20, 30, 10,
    0, 0, 0, 0
  ),
  "closed has a zero row: Pulse treats it as absorbing (unit row) with a warning; absorbing = closed.",
  absorbing = c("closed")
)

set.seed(2802)
sticky <- matrix(rpois(36, lambda = 3) + 1, nrow = 6)
diag(sticky) <- diag(sticky) + 400
mk_cases[[length(mk_cases) + 1]] <- mk_case(
  "sticky_seeded_6",
  paste0("s", 1:6),
  t(sticky),
  "Seeded sticky chain (Poisson(3) + 1 off the diagonal, + 400 on it), set.seed(2802): slow power-iteration convergence.",
  steps = c(1, 50, 1000)
)

write_golden("markov", list(
  generator = "scripts/reference/gen_markov.R",
  r_version = R.version.string,
  packages = list(
    markovchain = as.character(packageVersion("markovchain")),
    stats = as.character(packageVersion("stats")),
    jsonlite = as.character(packageVersion("jsonlite"))
  ),
  primitive = "OVERLAY_MARKOV",
  r_function = "markovchain: new('markovchain'), ^, steadyStates, is.irreducible, period, communicatingClasses, absorbingStates, meanAbsorptionTime, absorptionProbabilities",
  cases = mk_cases
))
