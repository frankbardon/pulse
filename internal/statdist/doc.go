// Package statdist holds the distribution primitives shared by the
// statistical operators: the Student-t tail, CDF, quantile and two-sided
// critical value, the regularized incomplete beta they rest on, and the
// standard normal quantile (NormalQuantile).
//
// It is a LEAF (stdlib + the stdlib-only linalg/detmath; gonum is allowed) so both
// internal/processing (TEST_* operators, overlays) and
// internal/processing/regression (REG_* coefficient p-values, Bayes
// credible intervals, coefficient confidence intervals) import one
// implementation without a cycle.
// TestStatdistImportBoundary enforces the ceiling.
//
// Every primitive is checked against R to a relative 1e-10 by
// reference_oracle_test.go (goldens under
// internal/processing/testdata/reference/, regenerated with
// `make reference`).
//
// Bit identity: the Student-t family (StudentTTwoSidedP, StudentTCDF,
// StudentTQuantile, StudentTInverseTwoSided) and the incomplete beta /
// logBeta / LgammaCorrection beneath them run on linalg/detmath's Log,
// Log1p, Exp and Lgamma, never math.*, and every product that feeds an
// add or subtract is an explicit float64(...) barrier, so their bits are
// the same on every GOARCH and CPU (TestStatdist_StudentTBitsPinned,
// TestStatdist_NoFMAContraction). NormalQuantile is NOT on that
// contract: it still calls math.Erfc / Exp / Log and its products may
// contract (the gate exempts normal.go by name).
package statdist
