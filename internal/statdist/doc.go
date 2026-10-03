// Package statdist holds the distribution primitives shared by the
// statistical operators: the Student-t tail, CDF, quantile and two-sided
// critical value, and the regularized incomplete beta they rest on.
//
// It is a LEAF (stdlib only today; gonum is allowed) so both
// internal/processing (TEST_* operators, overlays) and
// internal/processing/regression (REG_* coefficient p-values, Bayes
// credible intervals) import one implementation without a cycle.
// TestStatdistImportBoundary enforces the ceiling.
//
// Every primitive is checked against R to a relative 1e-10 by
// reference_oracle_test.go (goldens under
// internal/processing/testdata/reference/, regenerated with
// `make reference`).
//
// Bit-identity note: the helpers keep their function boundaries
// (studentTBetaArgs, regularizedIncompleteBetaLog, logBeta,
// LgammaCorrection, betacf, newtonBracketed) on purpose. Go may fuse
// a*b+c into an FMA differently on arm64 once an operand is folded in
// across a boundary, so restructuring them can move results by an ulp.
package statdist
