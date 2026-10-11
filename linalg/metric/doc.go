// Package metric is Pulse's one registry of distance and similarity
// metrics: every metric is defined here once — its kernel, the vector
// kinds it suits and fits poorly, the direction(s) it can answer in,
// whether it needs fitted statistics, and its domain rules — and every
// consumer (the expression metric functions, MAT_DISTANCE,
// GROUP_KMEANS, the reference-comparison attributes) resolves a metric
// name through it. The manifest lists the registry under
// capabilities.matrix.metrics.
//
// It is a leaf over the standard library, Pulse's coded-error package
// and the parent linalg package (for the Sym inverse covariance
// mahalanobis takes); never anything under internal/
// (TestMetricImportBoundary).
//
// # The registry
//
// All returns the 15 v1 metrics in a fixed order:
//
//   - plain: euclidean, sq_euclidean, manhattan, chebyshev, cosine,
//     correlation, hellinger, aitchison;
//   - set / binary: jaccard, dice, hamming, overlap;
//   - fitted: std_euclidean (per-element SD), mahalanobis (inverse
//     covariance), chi_square (column masses).
//
// Fitted metrics never estimate anything: the caller fits the
// statistics (a fit pass over the cohort) and hands them in through
// Stats.
//
// # Direction contract
//
// Every metric declares its native Direction and the Forms it can
// produce. A consumer asks for the Form it needs (Resolve, ResolveFor);
// a metric with no such form is refused with
// PULSE_VECTOR_METRIC_UNKNOWN and is NEVER converted silently. cosine
// is the one metric with both forms: the similarity is the cosine of
// the angle, the distance 1 − cos clamped to [0, 2]. A missing metric
// name is PULSE_VECTOR_METRIC_REQUIRED.
//
// # NaN policy (one, pairwise-complete)
//
//   - NaN marks a missing element. Index i is dropped when either side
//     is NaN; the remaining "complete pairs" are what every kernel
//     reads. ±Inf is an ordinary IEEE value, never skipped.
//   - No complete pair gives NaN (zero overlap), with no error.
//   - The element-sum distances — euclidean, sq_euclidean, manhattan,
//     std_euclidean and hamming (Scaled) — are rescaled by
//     p / p_observed, R's stats::dist() convention: the sum s over the
//     p_observed complete pairs becomes s / (p_observed / p) before any
//     square root. chebyshev is a maximum, not a sum, and is not
//     rescaled (dist(method = "maximum") is not either).
//   - The compositional metrics (hellinger, aitchison, chi_square)
//     close the observed sub-composition over the complete pairs and
//     are not rescaled. cosine, correlation and the set similarities
//     are ratios over the complete pairs.
//   - mahalanobis is Complete: its quadratic form couples every
//     coordinate, and the inverse of a covariance sub-block is not the
//     sub-block of the inverse, so any missing element gives NaN.
//   - Unequal lengths are a *LengthMismatchError (errors.Is
//     ErrLengthMismatch), never NaN.
//
// # Domain rules
//
// hellinger, aitchison and chi_square read non-negative values; the
// set metrics read 0 / 1 values. A violation returns NaN together with
// a *DomainError (errors.Is ErrDomain) naming the element and the
// reason, so a consumer can null the row and count it, or refuse. An
// aitchison zero part (the log-ratio is undefined) is a DomainError
// with Reason ReasonZero — "NaN for that row, counted".
//
// # FMA-free arithmetic
//
// Every product that feeds an addition or subtraction is written
// float64(a*b): the Go specification forbids fusing an explicitly
// converted product into a fused multiply-add, so arm64 and amd64 give
// identical bits. Sums run left to right in element order. Never remove
// a conversion or reorder a loop: the operation order is the contract.
// One residual is stated rather than hidden: aitchison takes math.Log,
// which is assembly on amd64 and pure Go on arm64, so its last bits can
// differ across architectures (math.Sqrt is exempt: IEEE-754 requires
// correct rounding).
package metric
