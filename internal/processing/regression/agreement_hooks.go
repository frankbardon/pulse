package regression

// Agreement hooks: this package keeps its own copy of the Student-t
// primitives (importing processing would be a cycle), so processing's
// TestStudentTCopiesAgree reaches this copy through these two thin
// exported wrappers and compares it bit-for-bit with processing's.
// They exist only for that test.

// StudentTTwoSidedPForAgreement returns this package's P(|T| ≥ |t|).
func StudentTTwoSidedPForAgreement(t, df float64) float64 { return studentTTwoSidedP(t, df) }

// StudentTQuantileForAgreement returns this package's lower-tail
// Student-t quantile.
func StudentTQuantileForAgreement(p, df float64) float64 { return studentTQuantile(p, df) }
