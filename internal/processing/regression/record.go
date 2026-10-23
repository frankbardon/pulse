package regression

import "math"

// Record is the minimal contract the regression engines need from the
// parent processing.Record. Defining it locally keeps this subpackage
// free of the processing import and mirrors the pattern used by
// internal/processing/feature.
//
// processing.Record satisfies this interface; the orchestrator passes
// records through unchanged.
type Record interface {
	// NumericValue returns the value and true if present and non-null.
	// Returns (0, false) when the named field is null or absent on the
	// record.
	NumericValue(name string) (float64, bool)
}

// InputValue is THE numeric read of a regression input — target,
// predictor, covariance order_by / numeric cluster key, the refit and
// resample row reads and the scorer's columns: (v, true) only when the
// value is present, non-null AND finite. A NaN or ±Inf is missing data
// and drops its row listwise exactly like a null (the row never enters
// the fit or n_obs; a scored row reads a null score). Every row read in
// this package goes through it (TestNonFiniteInput_ReadsGoThroughInputValue)
// except the weight read, which classifies a non-finite weight itself
// (weighting.Classify: NaNInf, counted as an invalid weight).
func InputValue(rec Record, name string) (float64, bool) {
	v, ok := rec.NumericValue(name)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
