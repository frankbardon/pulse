# Frequency-weight reference for AGG_CI_LOWER / AGG_CI_UPPER
# (weighting-inferential E5-S1, .claude/reference/weighting.md
# "Reference fixtures"). Called by gen_weight_reference.py — never by CI.
#
# Base R only (stats): the unweighted normal-critical interval on the
# rep()-expanded rows, the definition of a frequency weight —
# mean ∓ qnorm((1 + conf) / 2) · sd / √n. Pinned: R 4.6.1 (refuses any
# other version).
#
# Usage: Rscript ci_reference.R <conf> <x> <f>
#   x a comma-separated list of doubles (shortest round-trip form), f
#   the matching non-negative integer frequency weights.
# Prints one line "R <ver>", then "<figure> <value>" lines at 17
# significant digits.
want <- "4.6.1"
have <- sprintf("%s.%s", R.version$major, R.version$minor)
if (have != want) stop(sprintf("R %s required, found %s", want, have))
args <- commandArgs(trailingOnly = TRUE)
if (length(args) != 3) stop("usage: ci_reference.R <conf> <x> <f>")
num <- function(s) as.numeric(strsplit(s, ",", fixed = TRUE)[[1]])
conf <- as.numeric(args[[1]])
e <- rep(num(args[[2]]), num(args[[3]]))
z <- qnorm((1 + conf) / 2)
se <- sd(e) / sqrt(length(e))
cat(sprintf("R %s\n", have))
out <- function(fig, v) cat(sprintf("%s %.17g\n", fig, v))
out("mean", mean(e))
out("stderr", se)
out("t_critical", z)
out("lower", mean(e) - z * se)
out("upper", mean(e) + z * se)
