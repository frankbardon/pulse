// Package statdist holds the distribution primitives shared by the
// statistical operators: the Student-t tail, CDF, quantile and two-sided
// critical value, the regularized incomplete beta they rest on, and the
// standard normal quantile (NormalQuantile), and the chi-square CDF /
// survival on the regularized incomplete gamma (ChiSquareCDF,
// ChiSquareSurvival, RegularizedGammaP) — the deterministic replacements
// for gonum's distuv.ChiSquared.CDF / UnitNormal.Quantile and the one
// chi-square implementation internal/processing's TEST_* operators use.
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
// Bit identity: every primitive here runs on linalg/detmath's Log,
// Log1p, Exp, Erfc and Lgamma, never math.*, and every product that
// feeds an add or subtract is an explicit float64(...) barrier, so the
// bits are the same on every GOARCH and CPU. That covers the Student-t
// family (StudentTTwoSidedP, StudentTCDF, StudentTQuantile,
// StudentTInverseTwoSided) and the incomplete beta / logBeta /
// LgammaCorrection beneath them (TestStatdist_StudentTBitsPinned), the
// chi-square CDF / survival (chisquare.go) and NormalQuantile
// (normal.go), each agreeing with gonum's distuv within the tolerance
// distuv_replacement_test.go documents
// (TestStatdist_ChiSquareAndNormalQuantileBitsPinned). ExpAMD64FMA
// (exp_amd64fma.go) is the one Exp that is not detmath's: a portable
// port of Go's amd64 FMA-path math.Exp for the REG_GLM logit / poisson
// links, pinned by TestStatdist_ExpAMD64FMA. No file is
// exempt: TestStatdist_NoFMAContraction holds the whole package.
package statdist
