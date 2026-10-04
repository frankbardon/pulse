# Hmisc wtd.quantile reference for the probability-weighted median /
# percentile (weighting-descriptive E2-S1/E2-S4,
# .claude/reference/weighting.md "Reference fixtures"). Called by
# gen_weight_reference.py — never by CI.
#
# Pinned: R 4.6.1, Hmisc 5.3.0 (refuses any other Hmisc version).
# Install the pin into a private library and point R_LIBS at it:
#
#   mkdir -p rlib
#   R_LIBS=rlib Rscript -e 'install.packages("remotes", repos = "https://cloud.r-project.org")' \
#     -e 'remotes::install_version("Hmisc", "5.3.0", repos = "https://cloud.r-project.org")'
#
# Usage: Rscript hmisc_quantile.R <probs> <x> <w>
#   each argument a comma-separated list of doubles (shortest round-trip
#   form, so R parses the very same binary64 values Python holds).
# Prints one line "R <ver>; Hmisc <ver>", then one quantile per line at
# 17 significant digits, from
#   wtd.quantile(x, w, probs, type = "quantile", normwt = TRUE).
suppressMessages(library(Hmisc))
want <- "5.3.0"
if (as.character(packageVersion("Hmisc")) != want) {
  stop(sprintf("Hmisc %s required, found %s", want, packageVersion("Hmisc")))
}
args <- commandArgs(trailingOnly = TRUE)
if (length(args) != 3) stop("usage: hmisc_quantile.R <probs> <x> <w>")
num <- function(s) as.numeric(strsplit(s, ",", fixed = TRUE)[[1]])
probs <- num(args[[1]])
x <- num(args[[2]])
w <- num(args[[3]])
q <- wtd.quantile(x, w, probs = probs, type = "quantile", normwt = TRUE)
cat(sprintf("R %s.%s; Hmisc %s\n", R.version$major, R.version$minor, packageVersion("Hmisc")))
cat(sprintf("%.17g", unname(q)), sep = "\n")
cat("\n")
